// Package restores provides offline, read-only validation for candidate Memento restores.
package restores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/uptrace/bun"
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
	result := Result{Status: "valid", Checks: make([]string, 0, 6)}
	err := db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := migrations.CurrentIn(ctx, tx); err != nil {
			return fmt.Errorf("migrations: %w", err)
		}
		result.Checks = append(result.Checks, "migrations")
		if err := migrations.ExtensionsIn(ctx, tx); err != nil {
			return fmt.Errorf("extensions: %w", err)
		}
		result.Checks = append(result.Checks, "extensions")
		if err := migrations.SetupConsistentIn(ctx, tx); err != nil {
			return fmt.Errorf("setup: %w", err)
		}
		result.Checks = append(result.Checks, "setup_and_sole_curator")
		if err := validateForeignKeys(ctx, tx); err != nil {
			return err
		}
		result.Checks = append(result.Checks, "foreign_keys")
		if err := validateProjections(ctx, tx); err != nil {
			return err
		}
		result.Checks = append(result.Checks, "projections")
		if err := validateSecurity(ctx, tx); err != nil {
			return err
		}
		result.Checks = append(result.Checks, "security_settings")
		return loadCounts(ctx, tx, &result.Counts)
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func validateForeignKeys(ctx context.Context, db bun.IDB) error {
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
		constraint.conrelid::regclass,
		(SELECT string_agg(format('child.%I IS NOT NULL', child_column.attname), ' AND ' ORDER BY child_key.ordinality)
		 FROM unnest(constraint.conkey) WITH ORDINALITY AS child_key(attnum, ordinality)
		 JOIN pg_attribute AS child_column ON child_column.attrelid = constraint.conrelid
		  AND child_column.attnum = child_key.attnum),
		constraint.confrelid::regclass,
		(SELECT string_agg(format('parent.%I = child.%I', parent_column.attname, child_column.attname),
		 ' AND ' ORDER BY child_key.ordinality)
		 FROM unnest(constraint.conkey) WITH ORDINALITY AS child_key(attnum, ordinality)
		 JOIN unnest(constraint.confkey) WITH ORDINALITY AS parent_key(attnum, ordinality)
		  ON parent_key.ordinality = child_key.ordinality
		 JOIN pg_attribute AS child_column ON child_column.attrelid = constraint.conrelid
		  AND child_column.attnum = child_key.attnum
		 JOIN pg_attribute AS parent_column ON parent_column.attrelid = constraint.confrelid
		  AND parent_column.attnum = parent_key.attnum))
		FROM pg_constraint AS constraint
		WHERE constraint.contype = 'f' AND constraint.connamespace = current_schema()::regnamespace
		ORDER BY constraint.conrelid::regclass::text, constraint.conname`).Scan(ctx, &checks); err != nil {
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
		 WHERE event.current_publication_id <> current.publication_id OR publication.event_id <> current.event_id
		  OR revision.event_id <> current.event_id OR current.title IS DISTINCT FROM revision.title
		  OR current.description IS DISTINCT FROM revision.description
		  OR current.grouping_timezone IS DISTINCT FROM revision.grouping_timezone
		  OR current.place_labels IS DISTINCT FROM revision.place_labels) +
		(SELECT count(*) FROM events AS event WHERE event.current_publication_id IS NOT NULL AND NOT EXISTS (
		 SELECT 1 FROM current_published_events AS current
		 WHERE current.event_id = event.id AND current.publication_id = event.current_publication_id)) +
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
		(SELECT count(*) FROM current_audience_entitlements AS entitlement
		 WHERE NOT EXISTS (
		  SELECT 1 FROM current_published_placements AS placement
		  JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		  JOIN audience_entries AS audience ON audience.published_moment_id = moment.id
		   AND audience.recipient_access_generation_id = entitlement.recipient_access_generation_id
		  WHERE placement.event_id = entitlement.event_id AND placement.publication_id = entitlement.publication_id
		   AND placement.media_item_id = entitlement.media_item_id
		 )) +
		(SELECT count(*) FROM audience_entries AS audience
		 JOIN published_moments AS moment ON moment.id = audience.published_moment_id
		 JOIN current_published_events AS current ON current.publication_id = moment.publication_id
		 JOIN published_media_placements AS media ON media.published_moment_id = moment.id
		 WHERE NOT EXISTS (SELECT 1 FROM current_audience_entitlements AS entitlement
		  WHERE entitlement.event_id = current.event_id AND entitlement.publication_id = current.publication_id
		   AND entitlement.recipient_access_generation_id = audience.recipient_access_generation_id
		   AND entitlement.media_item_id = media.media_item_id)) +
		(SELECT count(*) FROM current_recipient_event_covers AS cover
		 WHERE NOT EXISTS (SELECT 1 FROM current_audience_entitlements AS entitlement
		  WHERE entitlement.event_id = cover.event_id
		   AND entitlement.recipient_access_generation_id = cover.recipient_access_generation_id
		   AND entitlement.media_item_id = cover.media_item_id)) +
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
		 WHERE event.current_staged_update_id <> staged.id OR event.current_publication_id <> staged.base_publication_id)
	`).Scan(ctx, &invalid)
	if err != nil {
		return fmt.Errorf("projections: %w", err)
	}
	if invalid != 0 {
		return ErrProjections
	}
	return nil
}

func validateSecurity(ctx context.Context, db bun.IDB) error {
	var invalid int
	err := db.NewRaw(`SELECT
		(SELECT count(*) FROM system_settings WHERE id <> 1 OR octet_length(security_epoch) <> 32 OR
		 ((recovery_nonce_hash IS NULL) <> (recovery_started_at IS NULL)) OR
		 (recovery_hold AND recovery_released_at IS NOT NULL) OR
		 (NOT recovery_hold AND recovery_nonce_hash IS NOT NULL AND recovery_released_at IS NULL)) +
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
