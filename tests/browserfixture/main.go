package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/pkg/audiences"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/server"
	"github.com/robinjoseph08/memento/pkg/sessions"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/uptrace/bun"
)

const (
	fixtureProjectCount = 4
	fixtureSecret       = "browser-fixture-security-secret-32-bytes"
)

var (
	errFixtureConfiguration = errors.New("browser fixture configuration is incomplete")
	errFixtureDatabase      = errors.New("browser fixture database is unavailable")
	errFixtureMigrations    = errors.New("browser fixture migrations failed")
	errFixtureSeed          = errors.New("browser fixture seed failed")
	errFixtureServer        = errors.New("browser fixture server failed")
	errFixtureListener      = errors.New("browser fixture listener failed")
	errFixtureLoopback      = errors.New("browser fixture listener is not loopback-only")
	errFixtureShutdown      = errors.New("browser fixture shutdown failed")
	errFixtureCredentials   = errors.New("browser fixture Session credentials are invalid")
)

type availableDependency struct{}

func (availableDependency) Check(context.Context) error { return nil }

type healthyWorker struct{}

func (healthyWorker) Healthy(time.Duration) bool { return true }

type projectFixture struct {
	index             int
	credential        string
	sessionID         uuid.UUID
	looseID           uuid.UUID
	mediaID           uuid.UUID
	missingLooseID    uuid.UUID
	missingMediaID    uuid.UUID
	completedPersonID uuid.UUID
	completedAccessID uuid.UUID
	pendingPersonID   uuid.UUID
	pendingAccessID   uuid.UUID
	deniedPersonID    uuid.UUID
	deniedAccessID    uuid.UUID
}

func main() {
	if run() != nil {
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("MEMENTO_TEST_DATABASE_URL")
	publicOrigin := os.Getenv("MEMENTO_TEST_BROWSER_PUBLIC_ORIGIN")
	if databaseURL == "" || publicOrigin == "" {
		return errFixtureConfiguration
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openFixtureDatabase(ctx, databaseURL)
	if err != nil {
		return errFixtureDatabase
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return errFixtureDatabase
	}
	if err := migrations.Apply(ctx, db); err != nil {
		return errFixtureMigrations
	}
	if err := seed(ctx, db); err != nil {
		return errFixtureSeed
	}

	security := config.SecurityConfig{
		Secret:                     fixtureSecret,
		SetupRateWindow:            time.Minute,
		SetupEmailLimit:            10,
		SetupIPLimit:               10,
		InvitationAcceptRateWindow: time.Minute,
		InvitationAcceptIPLimit:    10,
		SignInRateWindow:           time.Minute,
		SignInEmailLimit:           10,
		SignInIPLimit:              10,
	}
	setupService := setup.New(db, nil, security)
	eventService := events.New(db)
	sourceService := sources.New(db, nil, time.Hour)
	sessionService := sessions.New(db, nil, setupService, security)
	healthService := health.New(db, availableDependency{}, healthyWorker{}, time.Second, time.Minute)
	e, err := server.New(publicOrigin, server.RouteHandlers{
		Health:    healthService,
		Setup:     setup.NewHandler(setupService),
		Sources:   sources.NewHandler(sourceService, setupService),
		Events:    events.NewHandler(eventService, setupService),
		Audiences: audiences.NewHandler(audiences.New(db, nil), setupService),
		Sessions:  sessions.NewHandler(sessionService, setupService),
	})
	if err != nil {
		return errFixtureServer
	}

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return errFixtureListener
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		return errFixtureLoopback
	}

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- e.Server.Serve(listener)
	}()
	fmt.Printf("MEMENTO_BROWSER_API_URL=http://127.0.0.1:%d\n", address.Port)

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-signalCtx.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return errFixtureServer
		}
		return nil
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		return errFixtureShutdown
	}
	return nil
}

func seed(ctx context.Context, db *bun.DB) error {
	var credentials []string
	if err := json.Unmarshal([]byte(os.Getenv("MEMENTO_TEST_BROWSER_SESSION_CREDENTIALS")), &credentials); err != nil || len(credentials) != fixtureProjectCount {
		return errFixtureCredentials
	}
	now := time.Now().UTC()
	curatorID := fixtureID("90000000", 1)
	curatorAccessID := fixtureID("91000000", 1)
	if _, err := db.NewRaw(`
		UPDATE system_settings SET setup_complete = true, recovery_hold = false, updated_at = ? WHERE id = 1;
		INSERT INTO people (id, display_name, sort_name, created_at, updated_at)
		VALUES (?, 'Browser Curator', 'Browser Curator', ?, ?);
		INSERT INTO person_roles (person_id, role) VALUES (?, 'curator'), (?, 'recipient');
		INSERT INTO recipient_access_generations
			(id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at)
		VALUES (?, ?, 1, 'completed', true, ?, ?, ?)
	`, now, curatorID, now, now, curatorID, curatorID,
		curatorAccessID, curatorID, now, now, now).Exec(ctx); err != nil {
		return err
	}

	fixtures := make([]projectFixture, 0, fixtureProjectCount)
	for index := 1; index <= fixtureProjectCount; index++ {
		fixture, err := newProjectFixture(index, credentials[index-1])
		if err != nil {
			return err
		}
		fixtures = append(fixtures, fixture)
		credentialRaw, err := hex.DecodeString(fixture.credential)
		if err != nil {
			return err
		}
		credentialHash := sha256.Sum256(credentialRaw)
		if _, err := db.NewRaw(`
			INSERT INTO sessions
				(id, credential_hash, person_id, recipient_access_generation_id, security_epoch,
				 session_type, created_at, last_activity_at, idle_expires_at, label, browser, platform)
			SELECT ?, ?, ?, ?, security_epoch, 'trusted', ?, ?, ?, ?, 'Playwright', 'Browser fixture'
			FROM system_settings WHERE id = 1;
			INSERT INTO people (id, display_name, sort_name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?), (?, ?, ?, ?, ?);
			INSERT INTO person_roles (person_id, role)
			VALUES (?, 'recipient'), (?, 'recipient'), (?, 'recipient');
			INSERT INTO recipient_access_generations
				(id, person_id, generation, state, is_current, onboarding_completed_at, created_at, updated_at)
			VALUES (?, ?, 1, 'completed', true, ?, ?, ?),
				(?, ?, 1, 'pending', true, NULL, ?, ?),
				(?, ?, 1, 'completed', true, ?, ?, ?);
			INSERT INTO media_items
				(id, immich_asset_id, media_type, width, height, local_date_time, first_seen_at, last_seen_at)
			VALUES (?, ?, 'image', 1200, 800, '2026-08-01T09:00:00Z', ?, ?),
			 (?, ?, 'image', 1200, 800, '2026-08-01T10:00:00Z', ?, ?);
			UPDATE media_items SET availability = 'source_missing', missing_since = ? WHERE id = ?;
			INSERT INTO media_backings (id, media_item_id, immich_asset_id, linked_at)
			VALUES (?, ?, ?, ?), (?, ?, ?, ?);
			INSERT INTO loose_items
				(id, media_item_id, title, description, grouping_timezone, proposed_day, place_labels, created_at, updated_at)
			VALUES (?, ?, ?, 'Published description', 'UTC', '2026-08-01', ARRAY['Garden'], ?, ?),
			 (?, ?, ?, 'Unavailable source', 'UTC', '2026-08-01', ARRAY['Garden'], ?, ?)
		`, fixture.sessionID, credentialHash[:], curatorID, curatorAccessID,
			now, now, now.Add(24*time.Hour), fmt.Sprintf("Browser project %d", index),
			fixture.completedPersonID, fmt.Sprintf("Project %d Recipient", index), fmt.Sprintf("%02d Completed", index), now, now,
			fixture.pendingPersonID, fmt.Sprintf("Project %d Pending", index), fmt.Sprintf("%02d Pending", index), now, now,
			fixture.deniedPersonID, fmt.Sprintf("Project %d Denied", index), fmt.Sprintf("%02d Denied", index), now, now,
			fixture.completedPersonID, fixture.pendingPersonID, fixture.deniedPersonID,
			fixture.completedAccessID, fixture.completedPersonID, now, now, now,
			fixture.pendingAccessID, fixture.pendingPersonID, now, now,
			fixture.deniedAccessID, fixture.deniedPersonID, now, now, now,
			fixture.mediaID, fixtureID("61000000", index), now, now,
			fixture.missingMediaID, fixtureID("63000000", index), now, now,
			now, fixture.missingMediaID,
			fixtureID("62000000", index), fixture.mediaID, fixtureID("61000000", index), now,
			fixtureID("64000000", index), fixture.missingMediaID, fixtureID("63000000", index), now,
			fixture.looseID, fixture.mediaID, fmt.Sprintf("Garden portrait project %d", index), now, now,
			fixture.missingLooseID, fixture.missingMediaID, fmt.Sprintf("Missing source project %d", index), now, now,
		).Exec(ctx); err != nil {
			return err
		}
	}

	audienceService := audiences.New(db, nil)
	eventService := events.New(db)
	for _, fixture := range fixtures {
		actor := setup.CuratorSession{PersonID: curatorID, SessionID: fixture.sessionID}
		review, err := audienceService.ReviewLooseItem(ctx, actor, fixture.looseID)
		if err != nil {
			return err
		}
		review, err = audienceService.SetOverride(ctx, actor, "loose_item", fixture.looseID, review.Version,
			audiences.OverrideRequest{RecipientPersonID: fixture.completedPersonID.String(), State: "included"})
		if err != nil {
			return err
		}
		review, err = audienceService.SetOverride(ctx, actor, "loose_item", fixture.looseID, review.Version,
			audiences.OverrideRequest{RecipientPersonID: fixture.pendingPersonID.String(), State: "included"})
		if err != nil {
			return err
		}
		if _, err := audienceService.Approve(ctx, actor, "loose_item", fixture.looseID, review.Version); err != nil {
			return err
		}
		missingReview, err := audienceService.ReviewLooseItem(ctx, actor, fixture.missingLooseID)
		if err != nil {
			return err
		}
		if _, err := audienceService.Approve(ctx, actor, "loose_item", fixture.missingLooseID, missingReview.Version); err != nil {
			return err
		}
		item, err := eventService.GetLooseItem(ctx, actor, fixture.looseID)
		if err != nil {
			return err
		}
		if _, err := eventService.PublishLooseItem(ctx, actor, fixture.looseID,
			events.PublishLooseItemRequest{Version: item.Version}); err != nil {
			return err
		}
	}
	return nil
}

func newProjectFixture(index int, credential string) (projectFixture, error) {
	credentialRaw, err := hex.DecodeString(credential)
	if err != nil || len(credentialRaw) != 32 || strings.TrimSpace(credential) != credential {
		return projectFixture{}, errFixtureCredentials
	}
	return projectFixture{
		index:             index,
		credential:        credential,
		sessionID:         fixtureID("92000000", index),
		looseID:           fixtureID("10000000", index),
		mediaID:           fixtureID("20000000", index),
		missingLooseID:    fixtureID("11000000", index),
		missingMediaID:    fixtureID("21000000", index),
		completedPersonID: fixtureID("30000000", index),
		completedAccessID: fixtureID("31000000", index),
		pendingPersonID:   fixtureID("40000000", index),
		pendingAccessID:   fixtureID("41000000", index),
		deniedPersonID:    fixtureID("50000000", index),
		deniedAccessID:    fixtureID("51000000", index),
	}, nil
}

func fixtureID(prefix string, index int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("%s-0000-4000-8000-%012d", prefix, index))
}
