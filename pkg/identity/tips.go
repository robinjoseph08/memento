package identity

import (
	"context"
	"slices"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

// Tips lists every Tip. A new one also needs a migration that admits its name
// to persons.retired_tips.
var Tips = []Tip{TipVideos, TipJoin, TipChapters}

// RetireTip records that the signed-in Person dismissed or used a Tip, so no
// device shows it to them again. Retiring one twice changes nothing.
func (m *Module) RetireTip(ctx context.Context, token string, tip Tip) (Person, error) {
	var result Person
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		if !slices.Contains(Tips, tip) {
			return errcodes.NotFound("Tip")
		}
		if !slices.Contains(person.RetiredTips, string(tip)) {
			person.RetiredTips = append(person.RetiredTips, string(tip))
			if _, err := tx.NewUpdate().Model(&person).Column("retired_tips").WherePK().Exec(ctx); err != nil {
				return errorstack.CaptureContext(ctx, err)
			}
		}
		result = projectPerson(person)
		return nil
	})
	return result, err
}

// ResetTips brings back every Tip for the signed-in Person. Only development
// installations expose it, so a developer can see the Tips again without
// creating another Person.
func (m *Module) ResetTips(ctx context.Context, token string) (Person, error) {
	var result Person
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		person, err := m.actor(ctx, tx, token, false)
		if err != nil {
			return err
		}
		person.RetiredTips = []string{}
		if _, err := tx.NewUpdate().Model(&person).Column("retired_tips").WherePK().Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = projectPerson(person)
		return nil
	})
	return result, err
}

func retiredTips(person models.Person) []Tip {
	result := make([]Tip, len(person.RetiredTips))
	for i, tip := range person.RetiredTips {
		result[i] = Tip(tip)
	}
	return result
}
