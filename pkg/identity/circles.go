package identity

import (
	"context"
	"database/sql"
	"errors"
	"uuid"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

const (
	choosePeople  = "Refresh the page and choose people from the list."
	chooseCircles = "Refresh the page and choose Circles from the list."
)

// ListCircles returns every Circle in name order with its members.
func (m *Module) ListCircles(ctx context.Context, token string) ([]Circle, error) {
	result := []Circle{}
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		var circles []models.Circle
		if err := tx.NewSelect().Model(&circles).OrderExpr("lower(circle.name), circle.id").Scan(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		var err error
		result, err = projectCircles(ctx, tx, circles)
		return err
	})
	return result, err
}

func (m *Module) CreateCircle(ctx context.Context, token string, request CircleRequest) (Circle, error) {
	var result Circle
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		circle := models.Circle{ID: models.NewUUIDv7(), CreatedAt: m.now().UTC()}
		var err error
		if circle.Name, err = uniqueCircleName(ctx, tx, circle.ID, request.Name); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&circle).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result = Circle{ID: circle.ID.String(), Name: circle.Name, Members: []Person{}}
		return nil
	})
	return result, err
}

func (m *Module) RenameCircle(ctx context.Context, token, id string, request CircleRequest) (Circle, error) {
	var result Circle
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		circle, err := circleByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if circle.Name, err = uniqueCircleName(ctx, tx, circle.ID, request.Name); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(&circle).Column("name").WherePK().Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		result, err = projectCircle(ctx, tx, circle)
		return err
	})
	return result, err
}

// DeleteCircle removes a Circle and its memberships. Its People stay.
func (m *Module) DeleteCircle(ctx context.Context, token, id string) error {
	return m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		circle, err := circleByID(ctx, tx, id)
		if err != nil {
			return err
		}
		_, err = tx.NewDelete().Model(&circle).WherePK().Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
}

// SetCircleMembers makes the given People exactly the Circle's members.
func (m *Module) SetCircleMembers(ctx context.Context, token, id string, request CircleMembersRequest) (Circle, error) {
	var result Circle
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		circle, err := circleByID(ctx, tx, id)
		if err != nil {
			return err
		}
		people, err := existingIDs(ctx, tx, (*models.Person)(nil), request.PersonIDs, "person_ids", choosePeople)
		if err != nil {
			return err
		}
		rows := make([]models.CircleMember, 0, len(people))
		for _, person := range people {
			rows = append(rows, models.CircleMember{CircleID: circle.ID, PersonID: person})
		}
		if err := replaceMemberships(ctx, tx, "circle_id", circle.ID, rows); err != nil {
			return err
		}
		result, err = projectCircle(ctx, tx, circle)
		return err
	})
	return result, err
}

// SetPersonCircles makes the given Circles exactly the ones a Person belongs to.
func (m *Module) SetPersonCircles(ctx context.Context, token, personID string, request PersonCirclesRequest) error {
	return m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		person, err := personByID(ctx, tx, personID)
		if err != nil {
			return err
		}
		circles, err := existingIDs(ctx, tx, (*models.Circle)(nil), request.CircleIDs, "circle_ids", chooseCircles)
		if err != nil {
			return err
		}
		rows := make([]models.CircleMember, 0, len(circles))
		for _, circle := range circles {
			rows = append(rows, models.CircleMember{CircleID: circle, PersonID: person.ID})
		}
		return replaceMemberships(ctx, tx, "person_id", person.ID, rows)
	})
}

// personCircles lists every Circle in name order and whether a Person
// belongs to it.
func personCircles(ctx context.Context, tx bun.Tx, personID models.UUID) ([]PersonCircle, error) {
	var rows []struct {
		ID     models.UUID
		Name   string
		Member bool
	}
	err := tx.NewSelect().Model((*models.Circle)(nil)).Column("circle.id", "circle.name").
		ColumnExpr("member.person_id IS NOT NULL AS member").
		Join("LEFT JOIN circle_members AS member ON member.circle_id = circle.id AND member.person_id = ?", personID).
		OrderExpr("lower(circle.name), circle.id").Scan(ctx, &rows)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	result := make([]PersonCircle, 0, len(rows))
	for _, row := range rows {
		result = append(result, PersonCircle{ID: row.ID.String(), Name: row.Name, Member: row.Member})
	}
	return result, nil
}

func circleByID(ctx context.Context, tx bun.Tx, id string) (models.Circle, error) {
	var circle models.Circle
	if _, err := uuid.Parse(id); err != nil {
		return circle, errcodes.NotFound("Circle")
	}
	err := tx.NewSelect().Model(&circle).Where("circle.id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return circle, errcodes.NotFound("Circle")
	}
	return circle, errorstack.CaptureContext(ctx, err)
}

// uniqueCircleName checks a name and that no other Circle uses it in any
// letter case. The unique index enforces the same rule; this turns it into a
// field error, and the installation lock in change keeps the check current.
func uniqueCircleName(ctx context.Context, tx bun.Tx, id models.UUID, value string) (string, error) {
	name, err := circleName(value)
	if err != nil {
		return "", err
	}
	taken, err := tx.NewSelect().Model((*models.Circle)(nil)).
		Where("lower(circle.name) = lower(?) AND circle.id <> ?", name, id).Exists(ctx)
	if err != nil {
		return "", errorstack.CaptureContext(ctx, err)
	}
	if taken {
		return "", fieldError("name", "Another Circle already has this name. Choose a different one.")
	}
	return name, nil
}

// existingIDs parses and deduplicates ids, refusing them with a field error
// when any is malformed or names no row of model.
func existingIDs(ctx context.Context, tx bun.Tx, model any, ids []string, field, message string) ([]models.UUID, error) {
	seen := make(map[models.UUID]bool, len(ids))
	parsed := []models.UUID{}
	for _, id := range ids {
		value, err := uuid.Parse(id)
		if err != nil {
			return nil, fieldError(field, message)
		}
		if !seen[models.UUID(value)] {
			seen[models.UUID(value)] = true
			parsed = append(parsed, models.UUID(value))
		}
	}
	if len(parsed) == 0 {
		return parsed, nil
	}
	count, err := tx.NewSelect().Model(model).Where("id IN (?)", bun.List(parsed)).Count(ctx)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	if count != len(parsed) {
		return nil, fieldError(field, message)
	}
	return parsed, nil
}

// replaceMemberships makes rows the complete memberships of one Circle or
// one Person, named by column and id.
func replaceMemberships(ctx context.Context, tx bun.Tx, column string, id models.UUID, rows []models.CircleMember) error {
	if _, err := tx.NewDelete().Model((*models.CircleMember)(nil)).Where("? = ?", bun.Ident(column), id).Exec(ctx); err != nil {
		return errorstack.CaptureContext(ctx, err)
	}
	if len(rows) == 0 {
		return nil
	}
	_, err := tx.NewInsert().Model(&rows).Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}

func projectCircle(ctx context.Context, tx bun.Tx, circle models.Circle) (Circle, error) {
	circles, err := projectCircles(ctx, tx, []models.Circle{circle})
	if err != nil {
		return Circle{}, err
	}
	return circles[0], nil
}

// projectCircles attaches each Circle's members in name order.
func projectCircles(ctx context.Context, tx bun.Tx, circles []models.Circle) ([]Circle, error) {
	result := make([]Circle, 0, len(circles))
	if len(circles) == 0 {
		return result, nil
	}
	ids := make([]models.UUID, 0, len(circles))
	for _, circle := range circles {
		ids = append(ids, circle.ID)
	}
	var members []struct {
		models.Person `bun:",extend"`
		CircleID      models.UUID
	}
	err := selectPeople(tx, &members).ColumnExpr("member.circle_id").
		Join("JOIN circle_members AS member ON member.person_id = person.id").
		Where("member.circle_id IN (?)", bun.List(ids)).
		OrderExpr("lower(person.display_name), person.id").Scan(ctx)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	byCircle := make(map[models.UUID][]Person, len(circles))
	for _, member := range members {
		byCircle[member.CircleID] = append(byCircle[member.CircleID], projectPerson(member.Person))
	}
	for _, circle := range circles {
		projected := Circle{ID: circle.ID.String(), Name: circle.Name, Members: []Person{}}
		projected.Members = append(projected.Members, byCircle[circle.ID]...)
		result = append(result, projected)
	}
	return result, nil
}
