package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// A Circle is a Curator-named set of People (ADR 0015). Names are
		// unique per Installation regardless of letter case, and deleting a
		// Circle takes its memberships with it. DDL stays raw; Bun has no
		// builder for expression indexes and checks.
		_, err := db.ExecContext(ctx, `
CREATE TABLE circles (
 id uuid PRIMARY KEY,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
 created_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX circles_name_idx ON circles(lower(name));
CREATE TABLE circle_members (
 circle_id uuid NOT NULL REFERENCES circles(id) ON DELETE CASCADE,
 person_id uuid NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
 PRIMARY KEY (circle_id, person_id)
);
CREATE INDEX circle_members_person_id_idx ON circle_members(person_id);
`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `DROP TABLE circle_members; DROP TABLE circles;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
