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
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/database"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/lifecycle"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/server"
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

	owner, err := leaseOwner()
	if err != nil {
		_ = db.Close()
		log.Error("worker identity generation failed")
		return err
	}
	jobWorker, err := worker.New(db, cfg.Worker, owner, nil)
	if err != nil {
		_ = db.Close()
		log.Err(err).Error("worker startup failed")
		return err
	}
	healthService := health.New(db, immichClient, jobWorker, cfg.Database.HealthTimeout, cfg.Worker.HeartbeatMaxAge)
	e, err := server.New(healthService)
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

func leaseOwner() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
