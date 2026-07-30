// Package restores provides offline, read-only validation for candidate Memento restores.
package restores

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/staging"
	"github.com/uptrace/bun"
)

// This release-defined digest covers table, constraint name, and normalized definition
// for every expected foreign key after all registered migrations.
const (
	expectedForeignKeyInventorySHA256  = "1b49cf8988b176531ad730e92b382e6f223f9bc2a931f509ef202f2bdfdf4963"
	expectedRecoveryDeliveryViewSHA256 = "6ef46fa32643821f6367cb7e25264bbd62d1ed32e76713519440e05f80af285f"
	expectedWithdrawalFunctionSHA256   = "82ee8848f421c74c31eb5c380df242243f12d9eff71695ac974f3375cbc940f8"
)

var (
	ErrForeignKeys = errors.New("foreign-key validation failed")
	ErrProjections = errors.New("projection validation failed")
	ErrSecurity    = errors.New("security-settings validation failed")
)

// Counts are representative, non-sensitive row counts from a candidate restore.
type Counts struct {
	People               int `json:"people"`
	RecipientGenerations int `json:"recipient_generations"`
	Sessions             int `json:"sessions"`
	Events               int `json:"events"`
	MediaItems           int `json:"media_items"`
	Publications         int `json:"publications"`
	Jobs                 int `json:"jobs"`
	OutboxEvents         int `json:"outbox_events"`
	EmailDeliveries      int `json:"email_deliveries"`
	PushSubscriptions    int `json:"push_subscriptions"`
	SecurityAudits       int `json:"security_audits"`
	PublicationAudits    int `json:"publication_audits"`
}

// Result is emitted only after every validation check succeeds in one read-only snapshot.
type Result struct {
	Status string   `json:"status"`
	Checks []string `json:"checks"`
	Counts Counts   `json:"counts"`
}

// Validate checks a candidate database in one repeatable-read, read-only transaction.
func Validate(ctx context.Context, db *bun.DB) (Result, error) {
	var result Result
	err := db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		result, err = validateSnapshot(ctx, tx)
		return err
	})
	return result, err
}

func validateSnapshot(ctx context.Context, db bun.IDB) (Result, error) {
	result := Result{Status: "valid", Checks: make([]string, 0, 6)}
	if err := migrations.Current(ctx, db); err != nil {
		return Result{}, fmt.Errorf("migrations: %w", err)
	}
	result.Checks = append(result.Checks, "migrations")
	if err := migrations.Extensions(ctx, db); err != nil {
		return Result{}, fmt.Errorf("extensions: %w", err)
	}
	result.Checks = append(result.Checks, "extensions")
	if err := migrations.SetupConsistent(ctx, db); err != nil {
		return Result{}, fmt.Errorf("setup: %w", err)
	}
	result.Checks = append(result.Checks, "setup_and_sole_curator")
	if err := validateForeignKeys(ctx, db); err != nil {
		return Result{}, err
	}
	result.Checks = append(result.Checks, "foreign_keys")
	if err := validateProjections(ctx, db); err != nil {
		return Result{}, err
	}
	result.Checks = append(result.Checks, "projections")
	if err := validateSecurity(ctx, db); err != nil {
		return Result{}, err
	}
	result.Checks = append(result.Checks, "security_settings")
	if err := loadCounts(ctx, db, &result.Counts); err != nil {
		return Result{}, err
	}
	return result, nil
}

func validateForeignKeys(ctx context.Context, db bun.IDB) error {
	var inventory string
	if err := db.NewRaw(`SELECT COALESCE(jsonb_agg(
		jsonb_build_array(conrelid::regclass::text, conname, pg_get_constraintdef(oid, true))
		ORDER BY conrelid::regclass::text, conname)::text, '[]')
		FROM pg_constraint
		WHERE contype = 'f' AND connamespace = current_schema()::regnamespace`).Scan(ctx, &inventory); err != nil {
		return fmt.Errorf("foreign keys: %w", err)
	}
	digest := sha256.Sum256([]byte(inventory))
	if hex.EncodeToString(digest[:]) != expectedForeignKeyInventorySHA256 {
		return ErrForeignKeys
	}
	var invalid int
	if err := db.NewRaw(`SELECT count(*) FROM pg_constraint
		WHERE contype = 'f' AND connamespace = current_schema()::regnamespace AND NOT convalidated`).Scan(ctx, &invalid); err != nil {
		return fmt.Errorf("foreign keys: %w", err)
	}
	if invalid != 0 {
		return ErrForeignKeys
	}
	var checks []string
	if err := db.NewRaw(`SELECT format(
		'SELECT EXISTS (SELECT 1 FROM %s AS child WHERE %s AND NOT EXISTS (SELECT 1 FROM %s AS parent WHERE %s))',
		fk.conrelid::regclass,
		(SELECT string_agg(format('child.%I IS NOT NULL', child_column.attname), ' AND ' ORDER BY child_key.ordinality)
		 FROM unnest(fk.conkey) WITH ORDINALITY AS child_key(attnum, ordinality)
		 JOIN pg_attribute AS child_column ON child_column.attrelid = fk.conrelid
		  AND child_column.attnum = child_key.attnum),
		fk.confrelid::regclass,
		(SELECT string_agg(format('parent.%I = child.%I', parent_column.attname, child_column.attname),
		 ' AND ' ORDER BY child_key.ordinality)
		 FROM unnest(fk.conkey) WITH ORDINALITY AS child_key(attnum, ordinality)
		 JOIN unnest(fk.confkey) WITH ORDINALITY AS parent_key(attnum, ordinality)
		  ON parent_key.ordinality = child_key.ordinality
		 JOIN pg_attribute AS child_column ON child_column.attrelid = fk.conrelid
		  AND child_column.attnum = child_key.attnum
		 JOIN pg_attribute AS parent_column ON parent_column.attrelid = fk.confrelid
		  AND parent_column.attnum = parent_key.attnum))
		FROM pg_constraint AS fk
		WHERE fk.contype = 'f' AND fk.connamespace = current_schema()::regnamespace
		ORDER BY fk.conrelid::regclass::text, fk.conname`).Scan(ctx, &checks); err != nil {
		return fmt.Errorf("foreign keys: %w", err)
	}
	for _, query := range checks {
		var orphaned bool
		if err := db.NewRaw(query).Scan(ctx, &orphaned); err != nil {
			return fmt.Errorf("foreign keys: %w", err)
		}
		if orphaned {
			return ErrForeignKeys
		}
	}
	return nil
}

func validateProjections(ctx context.Context, db bun.IDB) error {
	var invalid int
	err := db.NewRaw(`SELECT
		(SELECT count(*) FROM current_published_events AS current
		 JOIN events AS event ON event.id = current.event_id
		 JOIN publications AS publication ON publication.id = current.publication_id
		 JOIN published_event_revisions AS revision ON revision.publication_id = current.publication_id
		 WHERE event.current_publication_id IS DISTINCT FROM current.publication_id
		  OR publication.event_id IS DISTINCT FROM current.event_id
		  OR revision.event_id IS DISTINCT FROM current.event_id OR current.title IS DISTINCT FROM revision.title
		  OR current.description IS DISTINCT FROM revision.description
		  OR current.grouping_timezone IS DISTINCT FROM revision.grouping_timezone
		  OR current.place_labels IS DISTINCT FROM revision.place_labels) +
		(SELECT count(*) FROM current_published_events AS current WHERE NOT EXISTS (
		 SELECT 1 FROM published_event_revisions AS revision
		 WHERE revision.publication_id = current.publication_id AND revision.event_id = current.event_id)) +
		(SELECT count(*) FROM events AS event WHERE event.current_publication_id IS NOT NULL AND NOT EXISTS (
		 SELECT 1 FROM current_published_events AS current
		 WHERE current.event_id = event.id AND current.publication_id = event.current_publication_id)) +
		(SELECT count(*) FROM (
		 SELECT current.event_id, media.media_item_id,
		  row_number() OVER (PARTITION BY current.event_id ORDER BY moment.position, media.position) - 1 AS expected_position
		 FROM current_published_events AS current
		 JOIN published_moments AS moment ON moment.publication_id = current.publication_id
		 JOIN published_media_placements AS media ON media.published_moment_id = moment.id
		) AS expected JOIN current_published_placements AS placement
		 ON placement.event_id = expected.event_id AND placement.media_item_id = expected.media_item_id
		 WHERE placement.position IS DISTINCT FROM expected.expected_position) +
		(SELECT count(*) FROM (
		 (SELECT moment.id AS published_moment_id, attendance.person_id
		  FROM current_published_events AS current
		  JOIN events AS event ON event.id = current.event_id
		  JOIN publications AS publication ON publication.id = current.publication_id
		  JOIN published_moments AS moment ON moment.publication_id = current.publication_id
		  JOIN attendance ON attendance.moment_id = moment.draft_moment_id
		  WHERE current.attendance_projection_ready AND event.version = publication.editable_version
		  EXCEPT
		  SELECT published_moment_id, person_id FROM published_attendance)
		 UNION ALL
		 (SELECT published.published_moment_id, published.person_id FROM published_attendance AS published
		  JOIN published_moments AS moment ON moment.id = published.published_moment_id
		  JOIN current_published_events AS current ON current.publication_id = moment.publication_id
		  JOIN events AS event ON event.id = current.event_id
		  JOIN publications AS publication ON publication.id = current.publication_id
		  WHERE current.attendance_projection_ready AND event.version = publication.editable_version
		  EXCEPT
		  SELECT moment.id, attendance.person_id
		  FROM current_published_events AS current
		  JOIN events AS event ON event.id = current.event_id
		  JOIN publications AS publication ON publication.id = current.publication_id
		  JOIN published_moments AS moment ON moment.publication_id = current.publication_id
		  JOIN attendance ON attendance.moment_id = moment.draft_moment_id
		  WHERE current.attendance_projection_ready AND event.version = publication.editable_version)
		) AS attendance_mismatch) +
		(SELECT count(*) FROM current_published_placements AS placement
		 WHERE NOT EXISTS (
		  SELECT 1 FROM current_published_events AS current
		  JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		  JOIN published_media_placements AS media ON media.published_moment_id = moment.id
		   AND media.media_item_id = placement.media_item_id
		  WHERE current.event_id = placement.event_id AND current.publication_id = placement.publication_id
		   AND moment.publication_id = placement.publication_id
		 )) +
		(SELECT count(*) FROM published_media_placements AS media
		 JOIN published_moments AS moment ON moment.id = media.published_moment_id
		 JOIN current_published_events AS current ON current.publication_id = moment.publication_id
		 WHERE NOT EXISTS (SELECT 1 FROM current_published_placements AS placement
		  WHERE placement.event_id = current.event_id AND placement.publication_id = current.publication_id
		   AND placement.published_moment_id = moment.id AND placement.media_item_id = media.media_item_id)) +
		(SELECT count(*) FROM content_withdrawals AS withdrawal WHERE
		 (withdrawal.target_kind = 'event' AND NOT EXISTS (SELECT 1 FROM events WHERE id = withdrawal.target_id)) OR
		 (withdrawal.target_kind = 'moment' AND NOT EXISTS (SELECT 1 FROM draft_moments WHERE id = withdrawal.target_id)) OR
		 (withdrawal.target_kind = 'media' AND NOT EXISTS (SELECT 1 FROM media_items WHERE id = withdrawal.target_id))) +
		(SELECT count(*) FROM current_audience_snapshots AS current
		 JOIN audience_snapshots AS snapshot ON snapshot.id = current.snapshot_id
		 WHERE current.target_kind IS DISTINCT FROM snapshot.target_kind
		  OR current.target_id IS DISTINCT FROM snapshot.target_id) +
		(SELECT count(*) FROM audience_proposals AS audience
		 JOIN recipient_access_generations AS access ON access.id = audience.recipient_access_generation_id
		 WHERE audience.recipient_person_id IS DISTINCT FROM access.person_id) +
		(SELECT count(*) FROM audience_snapshot_entries AS audience
		 JOIN recipient_access_generations AS access ON access.id = audience.recipient_access_generation_id
		 WHERE audience.recipient_person_id IS DISTINCT FROM access.person_id) +
		(SELECT count(*) FROM audience_entries AS audience
		 JOIN recipient_access_generations AS access ON access.id = audience.recipient_access_generation_id
		 WHERE audience.recipient_person_id IS DISTINCT FROM access.person_id) +
		(SELECT count(*) FROM audience_entries AS audience
		 JOIN published_moments AS moment ON moment.id = audience.published_moment_id
		 WHERE NOT EXISTS (SELECT 1 FROM audience_snapshot_entries AS snapshot
		  WHERE snapshot.snapshot_id = moment.audience_snapshot_id
		   AND snapshot.recipient_person_id = audience.recipient_person_id
		   AND snapshot.recipient_access_generation_id = audience.recipient_access_generation_id)) +
		(SELECT count(*) FROM published_moments AS moment
		 JOIN audience_snapshot_entries AS snapshot ON snapshot.snapshot_id = moment.audience_snapshot_id
		 WHERE NOT EXISTS (SELECT 1 FROM audience_entries AS audience
		  WHERE audience.published_moment_id = moment.id
		   AND audience.recipient_person_id = snapshot.recipient_person_id
		   AND audience.recipient_access_generation_id = snapshot.recipient_access_generation_id)) +
		(SELECT count(*) FROM current_audience_entitlements AS entitlement
		 JOIN recipient_access_generations AS access ON access.id = entitlement.recipient_access_generation_id
		 WHERE entitlement.recipient_person_id IS DISTINCT FROM access.person_id) +
		(SELECT count(*) FROM current_audience_entitlements AS entitlement
		 WHERE NOT EXISTS (
		  SELECT 1 FROM current_published_placements AS placement
		  JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		  JOIN audience_entries AS audience ON audience.published_moment_id = moment.id
		   AND audience.recipient_access_generation_id = entitlement.recipient_access_generation_id
		  WHERE placement.event_id = entitlement.event_id AND placement.publication_id = entitlement.publication_id
		   AND placement.media_item_id = entitlement.media_item_id
		 )) +
		(SELECT count(*) FROM current_audience_entitlements AS entitlement
		 JOIN publications AS publication ON publication.id = entitlement.publication_id
		 JOIN current_published_placements AS placement
		  ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
		 JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		 WHERE EXISTS (SELECT 1 FROM content_withdrawals AS withdrawal
		  WHERE withdrawal.restored_at IS NULL AND withdrawal.content_revision > publication.content_revision
		   AND ((withdrawal.target_kind = 'event' AND withdrawal.target_id = entitlement.event_id)
		    OR (withdrawal.target_kind = 'moment' AND withdrawal.target_id = moment.draft_moment_id)
		    OR (withdrawal.target_kind = 'media' AND withdrawal.target_id = entitlement.media_item_id)))) +
		(SELECT count(*) FROM audience_entries AS audience
		 JOIN published_moments AS moment ON moment.id = audience.published_moment_id
		 JOIN current_published_events AS current ON current.publication_id = moment.publication_id
		 JOIN published_media_placements AS media ON media.published_moment_id = moment.id
		 JOIN publications AS publication ON publication.id = current.publication_id
		 WHERE NOT EXISTS (SELECT 1 FROM content_withdrawals AS withdrawal
		  WHERE withdrawal.restored_at IS NULL AND withdrawal.content_revision > publication.content_revision
		   AND ((withdrawal.target_kind = 'event' AND withdrawal.target_id = current.event_id)
		    OR (withdrawal.target_kind = 'moment' AND withdrawal.target_id = moment.draft_moment_id)
		    OR (withdrawal.target_kind = 'media' AND withdrawal.target_id = media.media_item_id)))
		  AND NOT EXISTS (SELECT 1 FROM current_audience_entitlements AS entitlement
		   WHERE entitlement.event_id = current.event_id AND entitlement.publication_id = current.publication_id
		    AND entitlement.recipient_access_generation_id = audience.recipient_access_generation_id
		    AND entitlement.media_item_id = media.media_item_id)) +
		(SELECT count(*) FROM current_recipient_event_covers AS cover
		 WHERE NOT EXISTS (SELECT 1 FROM current_audience_entitlements AS entitlement
		  WHERE entitlement.event_id = cover.event_id
		   AND entitlement.recipient_access_generation_id = cover.recipient_access_generation_id
		   AND entitlement.media_item_id = cover.media_item_id)) +
		(SELECT count(*) FROM current_recipient_event_covers AS cover
		 WHERE cover.media_item_id IS DISTINCT FROM (
		  SELECT entitlement.media_item_id
		  FROM current_audience_entitlements AS entitlement
		  JOIN current_published_placements AS placement
		   ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
		  JOIN media_items AS media ON media.id = entitlement.media_item_id
		  JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		  WHERE entitlement.event_id = cover.event_id
		   AND entitlement.recipient_access_generation_id = cover.recipient_access_generation_id
		  ORDER BY (media.availability = 'current') DESC,
		   (moment.cover_media_item_id = entitlement.media_item_id) DESC, placement.position
		  LIMIT 1)) +
		(SELECT count(*) FROM (
		 SELECT event_id, recipient_access_generation_id FROM current_audience_entitlements GROUP BY 1, 2
		) AS expected_cover WHERE NOT EXISTS (
		 SELECT 1 FROM current_recipient_event_covers AS cover
		 WHERE cover.event_id = expected_cover.event_id
		  AND cover.recipient_access_generation_id = expected_cover.recipient_access_generation_id)) +
		(SELECT count(*) FROM published_search_documents AS document
		 JOIN current_published_events AS current ON current.event_id = document.event_id
		 JOIN current_published_placements AS placement
		  ON placement.event_id = document.event_id AND placement.media_item_id = document.media_item_id
		 JOIN published_media_placements AS published
		  ON published.published_moment_id = placement.published_moment_id
		  AND published.media_item_id = placement.media_item_id
		 JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		 WHERE document.search_text IS DISTINCT FROM concat_ws(' ', current.title, current.description)
		  OR document.capture_date IS DISTINCT FROM memento_local_capture_date(published.local_date_time)
		  OR document.place_text IS DISTINCT FROM array_to_string(current.place_labels || moment.place_labels, ' ')) +
		(SELECT count(*) FROM published_search_documents AS document
		 WHERE NOT EXISTS (SELECT 1 FROM current_audience_entitlements AS entitlement
		  WHERE entitlement.event_id = document.event_id AND entitlement.publication_id = document.publication_id
		   AND entitlement.recipient_access_generation_id = document.recipient_access_generation_id
		   AND entitlement.media_item_id = document.media_item_id)) +
		(SELECT count(*) FROM current_audience_entitlements AS entitlement
		 WHERE NOT EXISTS (SELECT 1 FROM published_search_documents AS document
		  WHERE document.event_id = entitlement.event_id AND document.publication_id = entitlement.publication_id
		   AND document.recipient_access_generation_id = entitlement.recipient_access_generation_id
		   AND document.media_item_id = entitlement.media_item_id)) +
		(SELECT count(*) FROM events AS event
		 WHERE (event.current_staged_update_id IS NULL) <> (NOT EXISTS (
		  SELECT 1 FROM staged_updates AS staged WHERE staged.event_id = event.id
		 ))) +
		(SELECT count(*) FROM staged_updates AS staged
		 JOIN events AS event ON event.id = staged.event_id
		 WHERE event.current_staged_update_id IS DISTINCT FROM staged.id
		  OR event.current_publication_id IS DISTINCT FROM staged.base_publication_id)
	`).Scan(ctx, &invalid)
	if err != nil {
		return fmt.Errorf("projections: %w", err)
	}
	if invalid != 0 {
		return ErrProjections
	}
	type stagedRow struct {
		EventID           uuid.UUID
		BasePublicationID uuid.UUID
		NetChanges        []byte
	}
	var stagedRows []stagedRow
	if err := db.NewRaw(`SELECT event_id, base_publication_id, net_changes FROM staged_updates ORDER BY event_id`).Scan(ctx, &stagedRows); err != nil {
		return fmt.Errorf("projections: %w", err)
	}
	for _, row := range stagedRows {
		if err := staging.ValidateUpdateProjection(ctx, db, row.EventID, row.BasePublicationID, row.NetChanges); err != nil {
			return ErrProjections
		}
	}
	return nil
}

func validateSecurity(ctx context.Context, db bun.IDB) error {
	var viewDefinition string
	if err := db.NewRaw(`SELECT pg_get_viewdef('recovery_curator_sign_in_deliveries'::regclass, true)`).Scan(ctx, &viewDefinition); err != nil {
		return fmt.Errorf("security settings: %w", err)
	}
	viewDigest := sha256.Sum256([]byte(viewDefinition))
	if hex.EncodeToString(viewDigest[:]) != expectedRecoveryDeliveryViewSHA256 {
		return ErrSecurity
	}
	var withdrawalDefinition string
	if err := db.NewRaw(`SELECT replace(pg_get_functiondef(function.oid), current_schema() || '.', '')
		FROM pg_proc AS function
		JOIN pg_namespace AS namespace ON namespace.oid = function.pronamespace
		WHERE namespace.oid = current_schema()::regnamespace
		 AND function.proname = 'content_is_withdrawn'
		 AND oidvectortypes(function.proargtypes) = 'uuid, uuid, uuid'`).Scan(ctx, &withdrawalDefinition); err != nil {
		return fmt.Errorf("security settings: %w", err)
	}
	withdrawalDigest := sha256.Sum256([]byte(withdrawalDefinition))
	if hex.EncodeToString(withdrawalDigest[:]) != expectedWithdrawalFunctionSHA256 {
		return ErrSecurity
	}
	var invalid int
	err := db.NewRaw(`SELECT
		(SELECT count(*) FROM system_settings WHERE id <> 1 OR octet_length(security_epoch) <> 32 OR
		 ((recovery_nonce_hash IS NULL) <> (recovery_started_at IS NULL)) OR
		 ((recovery_reviewed_at IS NULL) <> (recovery_reviewed_by_person_id IS NULL)) OR
		 ((recovery_reviewed_at IS NULL) <> (recovery_reviewed_by_session_id IS NULL)) OR
		 (recovery_hold AND recovery_released_at IS NOT NULL) OR
		 (NOT recovery_hold AND recovery_nonce_hash IS NOT NULL AND
		  (recovery_reviewed_at IS NULL OR recovery_released_at IS NULL)) OR
		 (recovery_nonce_hash IS NOT NULL AND NOT EXISTS (
		  SELECT 1 FROM recovery_nonce_history WHERE nonce_hash = system_settings.recovery_nonce_hash))) +
		(SELECT count(*) FROM recovery_nonce_history WHERE octet_length(nonce_hash) <> 32) +
		(SELECT count(*) FROM system_settings AS settings WHERE settings.setup_complete AND 1 <> (
		 SELECT count(*) FROM person_roles AS role
		 JOIN people AS person ON person.id = role.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		 JOIN recipient_access_generations AS access ON access.person_id = person.id
		  AND access.is_current AND access.state = 'completed'
		 JOIN recipient_emails AS email ON email.recipient_access_generation_id = access.id AND email.is_current
		 WHERE role.role = 'curator')) +
		(SELECT count(*) FROM (SELECT person_id FROM recipient_access_generations WHERE is_current GROUP BY person_id HAVING count(*) > 1) AS duplicate) +
		(SELECT count(*) FROM (SELECT normalized_email FROM recipient_emails WHERE is_current GROUP BY normalized_email HAVING count(*) > 1) AS duplicate) +
		(SELECT count(*) FROM sessions AS session WHERE octet_length(session.security_epoch) <> 32 OR NOT EXISTS (
		 SELECT 1 FROM recipient_access_generations AS access
		 WHERE access.id = session.recipient_access_generation_id AND access.person_id = session.person_id)) +
		(SELECT count(*) FROM sign_in_challenges WHERE octet_length(security_epoch) <> 32) +
		(SELECT count(*) FROM push_subscriptions AS push WHERE disabled_at IS NULL AND NOT EXISTS (
		 SELECT 1 FROM sessions AS session WHERE session.id = push.session_id AND session.person_id = push.person_id
		  AND session.session_type = 'trusted'))
	`).Scan(ctx, &invalid)
	if err != nil {
		return fmt.Errorf("security settings: %w", err)
	}
	if invalid != 0 {
		return ErrSecurity
	}
	return nil
}

func loadCounts(ctx context.Context, db bun.IDB, counts *Counts) error {
	return db.NewRaw(`SELECT
		(SELECT count(*) FROM people),
		(SELECT count(*) FROM recipient_access_generations),
		(SELECT count(*) FROM sessions),
		(SELECT count(*) FROM events),
		(SELECT count(*) FROM media_items),
		(SELECT count(*) FROM publications),
		(SELECT count(*) FROM jobs),
		(SELECT count(*) FROM outbox_events),
		(SELECT count(*) FROM email_deliveries),
		(SELECT count(*) FROM push_subscriptions),
		(SELECT count(*) FROM security_audit_events),
		(SELECT count(*) FROM publication_audit_events)`).Scan(ctx,
		&counts.People, &counts.RecipientGenerations, &counts.Sessions, &counts.Events,
		&counts.MediaItems, &counts.Publications, &counts.Jobs, &counts.OutboxEvents,
		&counts.EmailDeliveries, &counts.PushSubscriptions, &counts.SecurityAudits,
		&counts.PublicationAudits)
}
