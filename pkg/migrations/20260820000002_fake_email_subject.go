package migrations

import (
	"context"
	"strings"

	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var identities []models.Identity
			if err := tx.NewSelect().Model(&identities).Where("provider = ?", "fake").Scan(ctx); err != nil {
				return err
			}
			for _, linked := range identities {
				subject := strings.ToLower(strings.TrimSpace(linked.Email))
				if _, err := tx.NewUpdate().Model(&linked).Set("subject = ?", subject).WherePK().Exec(ctx); err != nil {
					return err
				}
			}
			return nil
		})
	}, func(context.Context, *bun.DB) error {
		// Email-derived fake subjects remain valid identifiers; their old values cannot be reconstructed.
		return nil
	})
}
