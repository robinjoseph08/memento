package migrations

import (
	"context"

	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		// The Tips a Person has retired, so each one shows once wherever they
		// sign in. A new Tip widens the check. DDL stays raw like the earlier
		// migrations.
		_, err := db.ExecContext(ctx, `ALTER TABLE persons ADD COLUMN retired_tips text[] NOT NULL DEFAULT '{}'
			CONSTRAINT persons_retired_tips_known CHECK (retired_tips <@ ARRAY['videos', 'join', 'chapters']::text[]);`)
		return errorstack.CaptureContext(ctx, err)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, `ALTER TABLE persons DROP COLUMN retired_tips;`)
		return errorstack.CaptureContext(ctx, err)
	})
}
