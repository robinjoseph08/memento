package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robinjoseph08/golib/logger"
	"github.com/robinjoseph08/memento/pkg/audiences"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/database"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/family"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/lifecycle"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/outbox"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/recipients"
	"github.com/robinjoseph08/memento/pkg/repairs"
	"github.com/robinjoseph08/memento/pkg/server"
	"github.com/robinjoseph08/memento/pkg/sessions"
	"github.com/robinjoseph08/memento/pkg/setup"
	mementosmtp "github.com/robinjoseph08/memento/pkg/smtp"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/robinjoseph08/memento/pkg/suggestions"
	"github.com/robinjoseph08/memento/pkg/visibility"
	"github.com/robinjoseph08/memento/pkg/worker"
)

func main() {
	if run() != nil {
		os.Exit(1)
	}
}

func run() error {
	log := logger.New()
	cfg, err := config.Load("")
	if err != nil {
		log.Err(err).Error("configuration is invalid")
		return err
	}

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStartup()
	db, err := database.Open(startupCtx, cfg.Database)
	if err != nil {
		log.Err(err).Error("Memento database connection failed")
		return err
	}
	if err := migrations.Apply(startupCtx, db); err != nil {
		_ = db.Close()
		log.Err(err).Error("database migration failed")
		return err
	}
	if err := migrations.Extensions(startupCtx, db); err != nil {
		_ = db.Close()
		log.Err(err).Error("required PostgreSQL extensions are unavailable")
		return err
	}

	immichClient, err := immich.New(cfg.Immich, nil)
	if err != nil {
		_ = db.Close()
		log.Error("Immich configuration is invalid")
		return err
	}
	if err := immichClient.Check(startupCtx); err != nil {
		log.Warn("Immich is not ready; liveness remains available")
	}

	var emailSender mementosmtp.Sender
	var deliveryHealth health.DeliveryStatus = mementosmtp.Disabled{}
	if cfg.SMTP.Enabled {
		smtpClient, smtpErr := mementosmtp.New(cfg.SMTP, nil)
		if smtpErr != nil {
			_ = db.Close()
			log.Error("SMTP configuration is invalid")
			return smtpErr
		}
		emailSender = smtpClient
		deliveryHealth = smtpClient
		if cfg.SMTP.InsecureDevelopment {
			log.Warn("insecure development SMTP transport is active")
		}
	}
	emailService := emaildelivery.New(db, cfg.SMTP, emailSender, cfg.Security.Secret)
	sourceService := sources.New(db, immichClient, cfg.Sources.ReconciliationInterval)
	handlers := jobHandlers(sourceService, emailService, cfg.SMTP.Enabled)

	owner, err := leaseOwner()
	if err != nil {
		_ = db.Close()
		log.Error("worker identity generation failed")
		return err
	}
	jobWorker, err := worker.New(db, cfg.Worker, owner, handlers, worker.WithDispatcher(outbox.New(db)))
	if err != nil {
		_ = db.Close()
		log.Err(err).Error("worker startup failed")
		return err
	}
	healthService := health.New(db, immichClient, jobWorker, cfg.Database.HealthTimeout, cfg.Worker.HeartbeatMaxAge, deliveryHealth)
	setupService := setup.New(db, emailService, cfg.Security)
	localGeoIP, err := sessions.OpenLocalGeoIP(cfg.GeoIP.DatabasePath)
	if err != nil {
		_ = db.Close()
		log.Err(err).Error("local GeoIP database is invalid")
		return err
	}
	if localGeoIP != nil {
		defer func() { _ = localGeoIP.Close() }()
		setupService.SetLocationResolver(localGeoIP)
	}
	setupHandler := setup.NewHandler(setupService)
	peopleService := people.New(db)
	peopleHandler := people.NewHandler(peopleService, setupService)
	familyHandler := family.NewHandler(family.New(db), setupService)
	visibilityHandler := visibility.NewHandler(visibility.New(db), setupService)
	recipientHandler := recipients.NewHandler(recipients.New(db, emailService, cfg.HTTP.PublicURL, setupService), setupService, cfg.Security)
	sourceHandler := sources.NewHandler(sourceService, setupService)
	eventHandler := events.NewHandler(events.New(db), setupService)
	repairHandler := repairs.NewHandler(repairs.New(db, immichClient), setupService)
	suggestionHandler := suggestions.NewHandler(suggestions.New(db, peopleService), setupService)
	audienceHandler := audiences.NewHandler(audiences.New(db, immichClient), setupService)
	sessionHandler := sessions.NewHandler(sessions.New(db, emailService, setupService, cfg.Security), setupService)
	e, err := server.New(healthService, emaildelivery.NewHandler(emailService), setupHandler, peopleHandler, familyHandler, visibilityHandler, recipientHandler, sourceHandler, eventHandler, repairHandler, suggestionHandler, audienceHandler, sessionHandler)
	if err != nil {
		_ = db.Close()
		log.Err(err).Error("HTTP server initialization failed")
		return err
	}

	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	jobWorker.Start(workCtx)

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- e.Start(cfg.HTTP.Address)
	}()

	select {
	case <-signalCtx.Done():
		log.Info("shutdown requested")
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			cancelWork()
			jobWorker.StopClaims()
			drainCtx, cancel := context.WithTimeout(context.Background(), cfg.Worker.DrainTimeout)
			_ = jobWorker.Drain(drainCtx)
			cancel()
			_ = db.Close()
			log.Err(err).Error("HTTP server stopped unexpectedly")
			return err
		}
		return nil
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancelShutdown()
	if err := lifecycle.Shutdown(shutdownCtx, cfg.Worker.DrainTimeout, healthService, e, jobWorker, db); err != nil {
		log.Err(err).Error("graceful shutdown exceeded its bounds")
		return err
	}
	log.Info("shutdown complete")
	return nil
}

func jobHandlers(sourceService *sources.Service, emailService *emaildelivery.Service, smtpEnabled bool) map[string]worker.Handler {
	handlers := map[string]worker.Handler{
		sources.ReconciliationJobKind: sourceService.HandleReconciliationJob,
	}
	if smtpEnabled {
		handlers[emaildelivery.JobKind] = emailService.Handle
	}
	return handlers
}

func leaseOwner() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
