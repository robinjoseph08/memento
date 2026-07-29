// Package notificationactivity owns channel-neutral immediate activity windows and final authorization.
package notificationactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/placementlock"
	"github.com/uptrace/bun"
)

const (
	CoalescingWindow            = 15 * time.Minute
	MaxBatchItems               = 100
	MaxPublicationMedia         = 100
	Publication         Kind    = "publication"
	Comment             Kind    = "comment"
	Email               Channel = "email"
	Push                Channel = "push"
)

var (
	ErrUnsupportedKind = errors.New("unsupported notification activity kind")
	ErrMalformedItem   = errors.New("malformed notification activity item")
	errInvalidChannel  = errors.New("invalid notification channel")
	errInvalidTarget   = errors.New("invalid notification target")
)

// Kind identifies one source of optional activity.
type Kind string

// Channel identifies an independent optional delivery path.
type Channel string

// Target identifies one independently coalesced delivery channel and device.
type Target struct {
	Channel            Channel
	JobKind            string
	PreferenceVersion  int64
	PushSubscriptionID *uuid.UUID
}

// JobPayload is the durable worker payload for an immediate batch.
type JobPayload struct {
	BatchID int64 `json:"batch_id"`
}

// Activity is one currently authorized item. Media identities remain server-side for email previews only.
type Activity struct {
	Kind              Kind
	SourceID          uuid.UUID
	Text              string
	Title             string
	AdditionCount     int
	Author            string
	MediaID           uuid.UUID
	AssetID           uuid.UUID
	ActivityCreatedAt time.Time
}

// AuthorizedSet is the bounded, ordered activity surviving current authorization.
type AuthorizedSet struct {
	AccessID   uuid.UUID
	Activities []Activity
	Truncated  bool
}

func (set AuthorizedSet) Empty() bool { return len(set.Activities) == 0 }

type pendingBatch struct {
	ID              int64
	PublicID        uuid.UUID
	WindowStartedAt time.Time
	Truncated       bool
}

type pendingItem struct {
	ID                int64
	BatchID           int64
	ActivityCreatedAt time.Time
	Incoming          bool
}

type plannedWindow struct {
	StartedAt time.Time
	Items     []pendingItem
	Truncated bool
}

// QueueImmediate assigns one source item to an exact 15-minute channel/device window.
func QueueImmediate(ctx context.Context, tx bun.Tx, accessID uuid.UUID, kind Kind, sourceID uuid.UUID, activityAt time.Time, target Target) error {
	column, err := sourceColumn(kind)
	if err != nil {
		return err
	}
	if target.Channel != Email && target.Channel != Push {
		return fmt.Errorf("%w: %q", errInvalidChannel, target.Channel)
	}
	if target.JobKind == "" || (target.Channel == Push) != (target.PushSubscriptionID != nil) {
		return errInvalidTarget
	}
	lockKey := accessID.String() + ":" + string(target.Channel)
	if target.PushSubscriptionID != nil {
		lockKey += ":" + target.PushSubscriptionID.String()
	}
	if _, err := tx.NewRaw(`SELECT pg_advisory_xact_lock(hashtextextended(?, 4610))`, lockKey).Exec(ctx); err != nil {
		return err
	}

	var alreadyQueued bool
	query := `SELECT EXISTS (SELECT 1 FROM notification_batch_items
		WHERE recipient_access_generation_id = ? AND channel = ? AND kind = ? AND ` + column + ` = ?`
	args := []any{accessID, target.Channel, kind, sourceID}
	if target.PushSubscriptionID == nil {
		query += ` AND push_subscription_id IS NULL)`
	} else {
		query += ` AND push_subscription_id = ?)`
		args = append(args, *target.PushSubscriptionID)
	}
	if err := tx.NewRaw(query, args...).Scan(ctx, &alreadyQueued); err != nil {
		return err
	}
	if alreadyQueued {
		return nil
	}

	batchQuery := `SELECT id, public_id, window_started_at, truncated FROM notification_batches
		WHERE recipient_access_generation_id = ? AND channel = ? AND cadence = 'immediate'
		  AND preference_version = ? AND status = 'pending'`
	batchArgs := []any{accessID, target.Channel, target.PreferenceVersion}
	if target.PushSubscriptionID == nil {
		batchQuery += ` AND push_subscription_id IS NULL`
	} else {
		batchQuery += ` AND push_subscription_id = ?`
		batchArgs = append(batchArgs, *target.PushSubscriptionID)
	}
	batchQuery += ` ORDER BY window_started_at, id FOR UPDATE`
	var batches []pendingBatch
	if err := tx.NewRaw(batchQuery, batchArgs...).Scan(ctx, &batches); err != nil {
		return err
	}
	batchIDs := make([]int64, 0, len(batches))
	truncatedByBatch := make(map[int64]bool, len(batches))
	for _, batch := range batches {
		batchIDs = append(batchIDs, batch.ID)
		truncatedByBatch[batch.ID] = batch.Truncated
	}
	var items []pendingItem
	if len(batchIDs) > 0 {
		if err := tx.NewRaw(`SELECT id, batch_id, activity_created_at FROM notification_batch_items
			WHERE batch_id IN (?) ORDER BY activity_created_at, id`, bun.List(batchIDs)).Scan(ctx, &items); err != nil {
			return err
		}
	}
	activityAt = activityAt.UTC()
	items = append(items, pendingItem{ActivityCreatedAt: activityAt, Incoming: true})
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].ActivityCreatedAt.Before(items[right].ActivityCreatedAt)
	})

	windows := make([]plannedWindow, 0, len(batches)+1)
	for _, item := range items {
		if len(windows) == 0 || !item.ActivityCreatedAt.Before(windows[len(windows)-1].StartedAt.Add(CoalescingWindow)) {
			windows = append(windows, plannedWindow{StartedAt: item.ActivityCreatedAt})
		}
		window := &windows[len(windows)-1]
		window.Items = append(window.Items, item)
		window.Truncated = window.Truncated || truncatedByBatch[item.BatchID]
	}

	for index, batch := range batches {
		startedAt := windows[index].StartedAt
		if _, err := tx.NewRaw(`UPDATE notification_batches SET window_started_at = ?, closes_at = ?, updated_at = clock_timestamp() WHERE id = ?`, startedAt, startedAt.Add(CoalescingWindow), batch.ID).Exec(ctx); err != nil {
			return err
		}
	}
	for len(batches) < len(windows) {
		batch, err := createBatch(ctx, tx, accessID, windows[len(batches)].StartedAt, target)
		if err != nil {
			return err
		}
		batches = append(batches, batch)
	}
	for index, window := range windows {
		batch := batches[index]
		closesAt := window.StartedAt.Add(CoalescingWindow)
		truncated := window.Truncated || len(window.Items) > MaxBatchItems
		if _, err := tx.NewRaw(`UPDATE notification_batches SET window_started_at = ?, closes_at = ?, truncated = ?, updated_at = clock_timestamp() WHERE id = ?`, window.StartedAt, closesAt, truncated, batch.ID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE outbox_events SET available_at = ?
			WHERE kind = ? AND aggregate_kind = 'notification_batch' AND aggregate_id = ? AND delivered_at IS NULL`, closesAt, target.JobKind, batch.PublicID.String()).Exec(ctx); err != nil {
			return err
		}
		itemIDs := make([]int64, 0, len(window.Items))
		hasIncoming := false
		for _, item := range window.Items {
			if item.Incoming {
				hasIncoming = true
			} else {
				itemIDs = append(itemIDs, item.ID)
			}
		}
		if len(itemIDs) > 0 {
			if _, err := tx.NewRaw(`UPDATE notification_batch_items SET batch_id = ? WHERE id IN (?)`, batch.ID, bun.List(itemIDs)).Exec(ctx); err != nil {
				return err
			}
		}
		if hasIncoming && len(itemIDs) < MaxBatchItems {
			if _, err := tx.NewRaw(`INSERT INTO notification_batch_items
				(batch_id, recipient_access_generation_id, channel, push_subscription_id, kind, `+column+`, activity_created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, batch.ID, accessID, target.Channel, target.PushSubscriptionID, kind, sourceID, activityAt).Exec(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func createBatch(ctx context.Context, tx bun.Tx, accessID uuid.UUID, startedAt time.Time, target Target) (pendingBatch, error) {
	batch := pendingBatch{PublicID: uuid.New(), WindowStartedAt: startedAt}
	closesAt := startedAt.Add(CoalescingWindow)
	if err := tx.NewRaw(`INSERT INTO notification_batches
		(public_id, recipient_access_generation_id, channel, push_subscription_id, cadence, preference_version, window_started_at, closes_at)
		VALUES (?, ?, ?, ?, 'immediate', ?, ?, ?) RETURNING id`, batch.PublicID, accessID, target.Channel, target.PushSubscriptionID, target.PreferenceVersion, startedAt, closesAt).Scan(ctx, &batch.ID); err != nil {
		return pendingBatch{}, err
	}
	payload, err := json.Marshal(JobPayload{BatchID: batch.ID})
	if err != nil {
		return pendingBatch{}, err
	}
	if _, err := tx.NewRaw(`INSERT INTO outbox_events
		(kind, aggregate_kind, aggregate_id, aggregate_version, payload, available_at, created_at)
		VALUES (?, 'notification_batch', ?, 1, ?::jsonb, ?, clock_timestamp())`, target.JobKind, batch.PublicID.String(), string(payload), closesAt).Exec(ctx); err != nil {
		return pendingBatch{}, err
	}
	return batch, nil
}

func sourceColumn(kind Kind) (string, error) {
	switch kind {
	case Publication:
		return "publication_id", nil
	case Comment:
		return "comment_id", nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedKind, kind)
	}
}

type batchItem struct {
	ID                int64
	Kind              Kind
	PublicationID     *uuid.UUID
	CommentID         *uuid.UUID
	ActivityCreatedAt time.Time
}

// AuthorizeBatch returns the exact bounded set that currently survives content and access policy.
func AuthorizeBatch(ctx context.Context, db bun.IDB, batchID int64, lock bool) (AuthorizedSet, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF access, person"
	}
	var result AuthorizedSet
	var truncated bool
	err := db.NewRaw(`SELECT access.id, batch.truncated
		FROM notification_batches AS batch
		JOIN recipient_access_generations AS access ON access.id = batch.recipient_access_generation_id
		 AND access.is_current AND access.state = 'completed'
		JOIN people AS person ON person.id = access.person_id AND person.archived_at IS NULL AND person.merged_at IS NULL
		WHERE batch.id = ? AND batch.cadence = 'immediate' AND batch.status = 'pending'`+lockClause, batchID).Scan(ctx, &result.AccessID, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthorizedSet{}, nil
	}
	if err != nil {
		return AuthorizedSet{}, err
	}
	var items []batchItem
	if err := db.NewRaw(`SELECT id, kind, publication_id, comment_id, activity_created_at
		FROM notification_batch_items WHERE batch_id = ? ORDER BY activity_created_at, id LIMIT ?`, batchID, MaxBatchItems+1).Scan(ctx, &items); err != nil {
		return AuthorizedSet{}, err
	}
	if len(items) > MaxBatchItems {
		items, truncated = items[:MaxBatchItems], true
	}
	result.Truncated = truncated
	result.Activities = make([]Activity, 0, len(items))
	for _, item := range items {
		var activity Activity
		var survives bool
		var err error
		switch item.Kind {
		case Publication:
			if item.PublicationID == nil {
				return AuthorizedSet{}, fmt.Errorf("%w: item %d has no publication", ErrMalformedItem, item.ID)
			}
			activity, survives, err = AuthorizePublication(ctx, db, result.AccessID, *item.PublicationID)
		case Comment:
			if item.CommentID == nil {
				return AuthorizedSet{}, fmt.Errorf("%w: item %d has no comment", ErrMalformedItem, item.ID)
			}
			activity, survives, err = AuthorizeComment(ctx, db, result.AccessID, *item.CommentID, lock)
		default:
			return AuthorizedSet{}, fmt.Errorf("%w: %q", ErrUnsupportedKind, item.Kind)
		}
		if err != nil {
			return AuthorizedSet{}, err
		}
		if survives {
			activity.ActivityCreatedAt = item.ActivityCreatedAt
			result.Activities = append(result.Activities, activity)
		}
	}
	return result, nil
}

// AuthorizePublication rechecks current title, additions, availability, entitlement, and Withdrawal.
func AuthorizePublication(ctx context.Context, db bun.IDB, accessID, publicationID uuid.UUID) (Activity, bool, error) {
	var title string
	var count int
	var mediaID, assetID uuid.UUID
	err := db.NewRaw(`WITH bounded_candidates AS MATERIALIZED (
		SELECT candidate.media_item_id FROM publication_notification_media AS candidate
		WHERE candidate.publication_id = ? AND candidate.recipient_access_generation_id = ?
		ORDER BY candidate.media_item_id LIMIT ?
	), authorized AS MATERIALIZED (
		SELECT DISTINCT candidate.media_item_id, media.immich_asset_id, placement.position
		FROM publications AS source JOIN bounded_candidates AS candidate ON true
		JOIN current_audience_entitlements AS entitlement ON entitlement.event_id = source.event_id
		 AND entitlement.recipient_access_generation_id = ? AND entitlement.media_item_id = candidate.media_item_id
		JOIN current_published_placements AS placement ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
		JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		JOIN media_items AS media ON media.id = candidate.media_item_id AND media.availability = 'current'
		WHERE source.id = ? AND source.notify_recipients
		 AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)
	)
	SELECT current.title, count(authorized.media_item_id),
	 (array_agg(authorized.media_item_id ORDER BY authorized.position, authorized.media_item_id))[1],
	 (array_agg(authorized.immich_asset_id ORDER BY authorized.position, authorized.media_item_id))[1]
	FROM publications AS source JOIN current_published_events AS current ON current.event_id = source.event_id
	JOIN authorized ON true WHERE source.id = ? GROUP BY current.title, source.id`, publicationID, accessID,
		MaxPublicationMedia+1, accessID, publicationID, publicationID).Scan(ctx, &title, &count, &mediaID, &assetID)
	if errors.Is(err, sql.ErrNoRows) {
		return Activity{}, false, nil
	}
	if err != nil {
		return Activity{}, false, err
	}
	if strings.TrimSpace(title) == "" {
		title = "A family event"
	}
	text := fmt.Sprintf("%s: %d new items", title, count)
	if count == 1 {
		text = title + ": 1 new item"
	} else if count > MaxPublicationMedia {
		text = fmt.Sprintf("%s: %d+ new items", title, MaxPublicationMedia)
	}
	return Activity{Kind: Publication, SourceID: publicationID, Text: text, Title: title,
		AdditionCount: count, MediaID: mediaID, AssetID: assetID}, count > 0, nil
}

// AuthorizeComment rechecks current comment state, subscription, access, availability, and Withdrawal.
func AuthorizeComment(ctx context.Context, db bun.IDB, accessID, commentID uuid.UUID, lock bool) (Activity, bool, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF comment"
		var subscriptionMediaID uuid.UUID
		err := db.NewRaw(`SELECT media_item_id FROM comment_subscriptions
			WHERE recipient_access_generation_id = ? AND media_item_id = (SELECT media_item_id FROM comments WHERE id = ?) FOR SHARE`, accessID, commentID).Scan(ctx, &subscriptionMediaID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Activity{}, false, err
		}
	}
	var createdAt time.Time
	var author string
	var mediaID, assetID uuid.UUID
	err := db.NewRaw(`SELECT comment.created_at, author.display_name, comment.media_item_id, media.immich_asset_id
		FROM comments AS comment
		JOIN recipient_access_generations AS access ON access.id = ? AND access.is_current AND access.state = 'completed'
		JOIN people AS recipient ON recipient.id = access.person_id AND recipient.archived_at IS NULL AND recipient.merged_at IS NULL
		JOIN people AS author ON author.id = comment.author_person_id
		JOIN media_items AS media ON media.id = comment.media_item_id AND media.availability = 'current'
		WHERE comment.id = ? AND comment.state = 'active' AND comment.author_person_id <> access.person_id
		AND (EXISTS (SELECT 1 FROM person_roles WHERE person_id = access.person_id AND role = 'curator')
		 OR EXISTS (SELECT 1 FROM comment_subscriptions AS subscription WHERE subscription.media_item_id = comment.media_item_id
		  AND subscription.recipient_access_generation_id = access.id AND NOT subscription.muted))
		AND EXISTS (SELECT 1 FROM current_published_placements AS placement
		 JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		 WHERE placement.media_item_id = comment.media_item_id
		 AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id))
		AND (EXISTS (SELECT 1 FROM person_roles WHERE person_id = access.person_id AND role = 'curator')
		 OR EXISTS (SELECT 1 FROM current_audience_entitlements AS entitlement
		  JOIN current_published_placements AS placement ON placement.event_id = entitlement.event_id AND placement.media_item_id = entitlement.media_item_id
		  JOIN published_moments AS moment ON moment.id = placement.published_moment_id
		  WHERE entitlement.recipient_access_generation_id = access.id AND entitlement.media_item_id = comment.media_item_id
		  AND NOT content_is_withdrawn(placement.event_id, moment.draft_moment_id, placement.media_item_id)))`+lockClause,
		accessID, commentID).Scan(ctx, &createdAt, &author, &mediaID, &assetID)
	if errors.Is(err, sql.ErrNoRows) {
		return Activity{}, false, nil
	}
	if err != nil {
		return Activity{}, false, err
	}
	return Activity{Kind: Comment, SourceID: commentID, Text: author + " commented on an item you can access.",
		Author: author, MediaID: mediaID, AssetID: assetID, ActivityCreatedAt: createdAt}, true, nil
}

// LockBatchResources orders Media and Event authorization mutations behind a final send.
func LockBatchResources(ctx context.Context, tx bun.Tx, batchID int64) error {
	if err := placementlock.Acquire(ctx, tx, placementlock.Shared); err != nil {
		return err
	}
	var mediaIDs []uuid.UUID
	if err := tx.NewRaw(`WITH selected_items AS MATERIALIZED (
		SELECT batch_id, kind, publication_id, comment_id FROM notification_batch_items
		WHERE batch_id = ? ORDER BY activity_created_at, id LIMIT ?
	) SELECT media.id FROM media_items AS media WHERE media.id IN (
		SELECT candidate.media_item_id FROM selected_items AS item
		JOIN notification_batches AS batch ON batch.id = item.batch_id
		JOIN LATERAL (SELECT source.media_item_id FROM publication_notification_media AS source
		 WHERE source.publication_id = item.publication_id AND source.recipient_access_generation_id = batch.recipient_access_generation_id
		 ORDER BY source.media_item_id LIMIT ?) AS candidate ON true WHERE item.kind = 'publication'
		UNION SELECT comment.media_item_id FROM selected_items AS item JOIN comments AS comment ON comment.id = item.comment_id WHERE item.kind = 'comment'
	) ORDER BY media.id FOR SHARE`, batchID, MaxBatchItems, MaxPublicationMedia+1).Scan(ctx, &mediaIDs); err != nil {
		return err
	}
	var eventIDs []uuid.UUID
	return tx.NewRaw(`WITH selected_items AS MATERIALIZED (
		SELECT kind, publication_id, comment_id FROM notification_batch_items WHERE batch_id = ? ORDER BY activity_created_at, id LIMIT ?
	) SELECT event.id FROM events AS event WHERE event.id IN (
		SELECT publication.event_id FROM selected_items AS item JOIN publications AS publication ON publication.id = item.publication_id WHERE item.kind = 'publication'
		UNION SELECT placement.event_id FROM selected_items AS item JOIN comments AS comment ON comment.id = item.comment_id
		JOIN current_published_placements AS placement ON placement.media_item_id = comment.media_item_id WHERE item.kind = 'comment'
	) ORDER BY event.id FOR SHARE`, batchID, MaxBatchItems).Scan(ctx, &eventIDs)
}
