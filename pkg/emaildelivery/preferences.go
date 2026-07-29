package emaildelivery

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// Preference is a Recipient's optional email choice and retained weekly schedule.
type Preference struct {
	EmailPreference string `json:"email_preference"`
	WeeklyDay       string `json:"weekly_day"`
	WeeklyLocalTime string `json:"weekly_local_time"`
	WeeklyTimezone  string `json:"weekly_timezone"`
}

// PreferenceUpdate changes optional email without changing identity email or Media access.
type PreferenceUpdate struct {
	EmailPreference string `json:"email_preference" validate:"required,oneof=immediate weekly none"`
	WeeklyDay       string `json:"weekly_day" validate:"required"`
	WeeklyLocalTime string `json:"weekly_local_time" validate:"required"`
	WeeklyTimezone  string `json:"weekly_timezone" validate:"required,max=255"`
}

func validatePreference(update PreferenceUpdate) error {
	if update.EmailPreference != "immediate" && update.EmailPreference != "weekly" && update.EmailPreference != "none" {
		return ErrNotificationPreference
	}
	_, err := parseWeeklySchedule(update.WeeklyDay, update.WeeklyLocalTime, update.WeeklyTimezone)
	return err
}

// PreferenceFor returns the current generation's optional email settings.
func (s *Service) PreferenceFor(ctx context.Context, accessID uuid.UUID) (Preference, error) {
	var preference Preference
	err := s.db.NewRaw(`
		SELECT preference.email_preference, preference.weekly_day,
		       preference.weekly_local_time,
		       CASE WHEN preference.weekly_schedule_overridden THEN preference.weekly_timezone
		            ELSE settings.weekly_timezone END
		FROM notification_preferences AS preference
		JOIN system_settings AS settings ON settings.id = 1
		JOIN recipient_access_generations AS access
		  ON access.id = preference.recipient_access_generation_id
		 AND access.is_current AND access.state = 'completed'
		WHERE preference.recipient_access_generation_id = ?
	`, accessID).Scan(ctx, &preference.EmailPreference, &preference.WeeklyDay,
		&preference.WeeklyLocalTime, &preference.WeeklyTimezone)
	if errors.Is(err, sql.ErrNoRows) {
		return Preference{}, ErrNotificationPreference
	}
	return preference, err
}

// UpdatePreference changes optional email settings for a current completed generation.
func (s *Service) UpdatePreference(ctx context.Context, accessID uuid.UUID, update PreferenceUpdate) (Preference, error) {
	if err := validatePreference(update); err != nil {
		return Preference{}, err
	}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return s.updatePreferenceIn(ctx, tx, accessID, update, true)
	})
	if err != nil {
		return Preference{}, err
	}
	return s.PreferenceFor(ctx, accessID)
}

func (s *Service) updatePreferenceIn(ctx context.Context, tx bun.Tx, accessID uuid.UUID, update PreferenceUpdate, requireCompleted bool) error {
	var current Preference
	var currentVersion int64
	stateClause := ""
	if requireCompleted {
		stateClause = " AND access.state = 'completed'"
	}
	err := tx.NewRaw(`
		SELECT preference.email_preference, preference.weekly_day,
		       preference.weekly_local_time, preference.weekly_timezone, preference.preference_version
		FROM notification_preferences AS preference
		JOIN recipient_access_generations AS access
		  ON access.id = preference.recipient_access_generation_id AND access.is_current`+stateClause+`
		WHERE preference.recipient_access_generation_id = ?
		FOR UPDATE OF preference, access
	`, accessID).Scan(ctx, &current.EmailPreference, &current.WeeklyDay,
		&current.WeeklyLocalTime, &current.WeeklyTimezone, &currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotificationPreference
	}
	if err != nil {
		return err
	}
	nextVersion := currentVersion
	if current.EmailPreference != update.EmailPreference {
		nextVersion++
	}
	if _, err := tx.NewRaw(`UPDATE notification_preferences
		SET email_preference = ?, weekly_day = ?, weekly_local_time = ?, weekly_timezone = ?,
		    weekly_schedule_overridden = true, preference_version = ?, updated_at = clock_timestamp()
		WHERE recipient_access_generation_id = ?`, update.EmailPreference, update.WeeklyDay,
		update.WeeklyLocalTime, update.WeeklyTimezone, nextVersion, accessID).Exec(ctx); err != nil {
		return err
	}
	return nil
}

// PlatformWeeklyTimezone returns the Curator-managed timezone used by schedules without an override.
func (s *Service) PlatformWeeklyTimezone(ctx context.Context) (string, error) {
	var timezone string
	err := s.db.NewRaw(`SELECT weekly_timezone FROM system_settings WHERE id = 1`).Scan(ctx, &timezone)
	return timezone, err
}

// UpdatePlatformWeeklyTimezone changes the default and invalidates pending default-schedule batches.
func (s *Service) UpdatePlatformWeeklyTimezone(ctx context.Context, timezone string) (string, error) {
	if _, err := parseWeeklySchedule("sunday", "09:00", timezone); err != nil {
		return "", err
	}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var current string
		if err := tx.NewRaw(`SELECT weekly_timezone FROM system_settings WHERE id = 1 FOR UPDATE`).Scan(ctx, &current); err != nil {
			return err
		}
		if current == timezone {
			return nil
		}
		_, err := tx.NewRaw(`UPDATE system_settings SET weekly_timezone = ?, updated_at = clock_timestamp()
			WHERE id = 1`, timezone).Exec(ctx)
		return err
	})
	if err != nil {
		return "", err
	}
	return timezone, nil
}
