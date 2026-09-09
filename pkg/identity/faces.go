package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/uptrace/bun"
)

func personAvatarURL(person models.Person) string {
	if person.AvatarFaceID == nil {
		return ""
	}
	return "/api/media/people/" + person.ID.String() + "/avatar?v=" + url.QueryEscape(*person.AvatarFaceID)
}

func faceAvailable(ctx context.Context, tx bun.Tx, sourceID string) (bool, error) {
	available, err := tx.NewSelect().Model((*models.MediaFaceAssociation)(nil)).Where("face.source_face_id = ?", sourceID).Exists(ctx)
	return available, errorstack.CaptureContext(ctx, err)
}

func linkFace(ctx context.Context, tx bun.Tx, person models.Person, sourceID string, now time.Time) error {
	sourceID = strings.TrimSpace(sourceID)
	available, err := faceAvailable(ctx, tx, sourceID)
	if err != nil {
		return err
	}
	if !available {
		return errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"source_face_id": "Refresh faces and choose one shown in this Album."})
	}
	var current models.ImmichFaceLink
	err = tx.NewSelect().Model(&current).Where("face_link.source_id = ?", sourceID).For("UPDATE").Scan(ctx)
	if err == nil && current.PersonID != nil && *current.PersonID != person.ID {
		return errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"source_face_id": "This Immich face is already linked to another Person."})
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return errorstack.CaptureContext(ctx, err)
	}
	personID := person.ID
	row := models.ImmichFaceLink{SourceID: sourceID, PersonID: &personID, UpdatedAt: now}
	_, err = tx.NewInsert().Model(&row).On("CONFLICT (source_id) DO UPDATE").
		Set("person_id = EXCLUDED.person_id").Set("ignored = false").Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
	return errorstack.CaptureContext(ctx, err)
}

func linkedFaces(ctx context.Context, tx bun.Tx, person models.Person) ([]LinkedFace, error) {
	type linkedFaceRow struct {
		SourceID   string
		SourceName string
	}
	rows := []linkedFaceRow{}
	err := tx.NewSelect().TableExpr("immich_face_links AS link").
		ColumnExpr("link.source_id, coalesce(min(face.source_name), '') AS source_name").
		Join("LEFT JOIN media_face_associations AS face ON face.source_face_id = link.source_id").
		Where("link.person_id = ? AND NOT link.ignored", person.ID).
		Group("link.source_id").Order("link.source_id").Scan(ctx, &rows)
	if err != nil {
		return nil, errorstack.CaptureContext(ctx, err)
	}
	result := make([]LinkedFace, 0, len(rows))
	for _, row := range rows {
		result = append(result, LinkedFace{SourceFaceID: row.SourceID, SourceName: row.SourceName,
			ThumbnailURL: "/api/media/faces/" + url.PathEscape(row.SourceID) + "/thumbnail", Avatar: person.AvatarFaceID != nil && *person.AvatarFaceID == row.SourceID})
	}
	return result, nil
}

func (m *Module) LinkFace(ctx context.Context, token, personID string, request LinkFaceRequest) (PersonDetail, error) {
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		person, err := personByID(ctx, tx, personID)
		if err != nil {
			return err
		}
		return linkFace(ctx, tx, person, request.SourceFaceID, m.now().UTC())
	})
	if err != nil {
		return PersonDetail{}, err
	}
	return m.GetPerson(ctx, token, personID)
}

func (m *Module) CreatePersonFromFace(ctx context.Context, token string, request CreatePersonFromFaceRequest) (PersonDetail, error) {
	var personID string
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		name, err := displayName(request.DisplayName)
		if err != nil {
			return err
		}
		person := models.Person{ID: models.NewUUIDv7(), DisplayName: name, CreatedAt: m.now().UTC()}
		if _, err := tx.NewInsert().Model(&person).Exec(ctx); err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if err := linkFace(ctx, tx, person, request.SourceFaceID, m.now().UTC()); err != nil {
			return err
		}
		personID = person.ID.String()
		return nil
	})
	if err != nil {
		return PersonDetail{}, err
	}
	return m.GetPerson(ctx, token, personID)
}

func (m *Module) IgnoreFace(ctx context.Context, token, sourceID string) error {
	return m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		sourceID = strings.TrimSpace(sourceID)
		available, err := faceAvailable(ctx, tx, sourceID)
		if err != nil {
			return err
		}
		if !available {
			return errcodes.ValidationError("Refresh faces and choose one shown in this Album.")
		}
		var current models.ImmichFaceLink
		err = tx.NewSelect().Model(&current).Where("face_link.source_id = ?", sourceID).For("UPDATE").Scan(ctx)
		if err == nil && current.PersonID != nil {
			return errcodes.ValidationError("This Immich face is already linked to a Person.")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errorstack.CaptureContext(ctx, err)
		}
		row := models.ImmichFaceLink{SourceID: sourceID, Ignored: true, UpdatedAt: m.now().UTC()}
		_, err = tx.NewInsert().Model(&row).On("CONFLICT (source_id) DO UPDATE").
			Set("person_id = NULL").Set("ignored = true").Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
}

func (m *Module) SetPersonAvatar(ctx context.Context, token, personID string, request SetPersonAvatarRequest) (PersonDetail, error) {
	err := m.change(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := m.actor(ctx, tx, token, true); err != nil {
			return err
		}
		person, err := personByID(ctx, tx, personID)
		if err != nil {
			return err
		}
		linked, err := tx.NewSelect().Model((*models.ImmichFaceLink)(nil)).
			Where("face_link.source_id = ? AND face_link.person_id = ? AND NOT face_link.ignored", strings.TrimSpace(request.SourceFaceID), person.ID).Exists(ctx)
		if err != nil {
			return errorstack.CaptureContext(ctx, err)
		}
		if !linked {
			return errcodes.ValidationFields("Check the highlighted fields.", map[string]string{"source_face_id": "Choose one of this Person's linked Immich faces."})
		}
		_, err = tx.NewUpdate().Model((*models.Person)(nil)).Set("avatar_face_id = ?", strings.TrimSpace(request.SourceFaceID)).Where("id = ?", person.ID).Exec(ctx)
		return errorstack.CaptureContext(ctx, err)
	})
	if err != nil {
		return PersonDetail{}, err
	}
	return m.GetPerson(ctx, token, personID)
}
