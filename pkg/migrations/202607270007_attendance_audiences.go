package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				statements := []string{
					`ALTER TABLE draft_moments ADD COLUMN review_version bigint NOT NULL DEFAULT 1 CHECK (review_version > 0)`,
					`UPDATE draft_moments SET attendance_complete = false, audience_complete = false`,
					`CREATE TABLE attendance (
						moment_id uuid NOT NULL,
						person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						source text NOT NULL CHECK (source IN ('manual', 'face_suggestion')),
						confirmed_by_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						confirmed_at timestamptz NOT NULL,
						PRIMARY KEY (moment_id, person_id)
					)`,
					`CREATE INDEX attendance_person_moment_idx ON attendance (person_id, moment_id)`,
					`CREATE TABLE audience_overrides (
						target_kind text NOT NULL CHECK (target_kind IN ('moment', 'loose_item')),
						target_id uuid NOT NULL,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						state text NOT NULL CHECK (state IN ('included', 'excluded')),
						updated_by_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						updated_at timestamptz NOT NULL,
						PRIMARY KEY (target_kind, target_id, recipient_person_id)
					)`,
					`CREATE INDEX audience_overrides_recipient_idx ON audience_overrides (recipient_person_id, target_kind, target_id)`,
					`CREATE TABLE audience_proposals (
						target_kind text NOT NULL CHECK (target_kind IN ('moment', 'loose_item')),
						target_id uuid NOT NULL,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						included boolean NOT NULL,
						recalculated_at timestamptz NOT NULL,
						PRIMARY KEY (target_kind, target_id, recipient_person_id)
					)`,
					`CREATE INDEX audience_proposals_generation_idx ON audience_proposals (recipient_access_generation_id, target_kind, target_id)`,
					`CREATE TABLE audience_reasons (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						target_kind text NOT NULL,
						target_id uuid NOT NULL,
						recipient_person_id uuid NOT NULL,
						kind text NOT NULL CHECK (kind IN ('present', 'interested', 'manually_included', 'manually_excluded')),
						matching_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						FOREIGN KEY (target_kind, target_id, recipient_person_id)
							REFERENCES audience_proposals(target_kind, target_id, recipient_person_id) ON DELETE CASCADE,
						CHECK ((kind IN ('present', 'interested')) = (matching_person_id IS NOT NULL))
					)`,
					`CREATE UNIQUE INDEX audience_reasons_unique_idx ON audience_reasons (
						target_kind, target_id, recipient_person_id, kind, matching_person_id
					) NULLS NOT DISTINCT`,
					`CREATE TABLE audience_snapshots (
						id uuid PRIMARY KEY,
						target_kind text NOT NULL CHECK (target_kind IN ('moment', 'loose_item')),
						target_id uuid NOT NULL,
						approved_by_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						approved_at timestamptz NOT NULL,
						label text NOT NULL CHECK (label IN ('Shared', 'Curator only'))
					)`,
					`CREATE INDEX audience_snapshots_target_idx ON audience_snapshots (target_kind, target_id, approved_at DESC)`,
					`CREATE TABLE audience_snapshot_entries (
						snapshot_id uuid NOT NULL REFERENCES audience_snapshots(id) ON DELETE RESTRICT,
						recipient_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						recipient_access_generation_id uuid NOT NULL REFERENCES recipient_access_generations(id) ON DELETE RESTRICT,
						PRIMARY KEY (snapshot_id, recipient_person_id)
					)`,
					`CREATE INDEX audience_snapshot_entries_generation_idx ON audience_snapshot_entries (recipient_access_generation_id, snapshot_id)`,
					`CREATE TABLE publication_audit_events (
						id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
						event_id uuid REFERENCES events(id) ON DELETE RESTRICT,
						target_kind text NOT NULL CHECK (target_kind IN ('moment', 'loose_item')),
						target_id uuid NOT NULL,
						actor_person_id uuid NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
						action text NOT NULL CHECK (action <> ''),
						metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
						created_at timestamptz NOT NULL DEFAULT now()
					)`,
					`CREATE INDEX publication_audit_event_time_idx ON publication_audit_events (event_id, created_at DESC) WHERE event_id IS NOT NULL`,
					`CREATE INDEX publication_audit_target_time_idx ON publication_audit_events (target_kind, target_id, created_at DESC)`,
					`CREATE INDEX publication_audit_actor_time_idx ON publication_audit_events (actor_person_id, created_at DESC)`,
					`CREATE TABLE current_audience_snapshots (
						target_kind text NOT NULL CHECK (target_kind IN ('moment', 'loose_item')),
						target_id uuid NOT NULL,
						snapshot_id uuid NOT NULL UNIQUE REFERENCES audience_snapshots(id) ON DELETE RESTRICT,
						PRIMARY KEY (target_kind, target_id)
					)`,
					`ALTER TABLE loose_items ADD COLUMN audience_complete boolean NOT NULL DEFAULT false, ADD COLUMN review_version bigint NOT NULL DEFAULT 1 CHECK (review_version > 0)`,
				}
				for _, statement := range statements {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, statement := range []string{
					`ALTER TABLE loose_items DROP COLUMN audience_complete, DROP COLUMN review_version`,
					`DROP TABLE current_audience_snapshots`,
					`DROP TABLE publication_audit_events`,
					`DROP TABLE audience_snapshot_entries`,
					`DROP TABLE audience_snapshots`,
					`DROP TABLE audience_reasons`,
					`DROP TABLE audience_proposals`,
					`DROP TABLE audience_overrides`,
					`DROP TABLE attendance`,
					`ALTER TABLE draft_moments DROP COLUMN review_version`,
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
	)
}
