package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/robinjoseph08/golib/logger"
	"github.com/robinjoseph08/golib/signals"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/database"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/robinjoseph08/memento/pkg/server"
	"github.com/robinjoseph08/memento/pkg/version"
)

const (
	shutdownHardDeadline  = 5 * time.Second
	serverShutdownTimeout = 3 * time.Second
)

func main() {
	if os.Getenv("LOG_FORMAT") == "" {
		logger.SetOutput(os.Stdout)
	}
	log := logger.New()
	if err := run(log); err != nil {
		log.Err(err).Fatal("application stopped")
	}
}

func run(log logger.Logger) error {
	ctx := context.Background()
	log.Info("starting application", logger.Data{"version": version.Version})

	cfg, err := config.New()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := database.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Err(errorstack.Capture(closeErr)).Error("database close error")
		}
	}()

	group, err := migrations.BringUpToDate(ctx, db)
	if err != nil {
		return fmt.Errorf("bring migrations up to date: %w", err)
	}
	if group.ID == 0 {
		log.Info("no new migrations to run")
	} else {
		log.Info("migrations applied", logger.Data{
			"group_id":        group.ID,
			"migration_names": group.Migrations.String(),
		})
	}

	srv, err := server.New(cfg, db)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("server started", logger.Data{"address": srv.Addr})
		err := srv.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			err = errorstack.Capture(err)
		}
		serverErrors <- err
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-signals.Setup():
		log.Info("starting graceful shutdown")
	}

	watchdog := time.AfterFunc(shutdownHardDeadline, func() {
		log.Error("graceful shutdown exceeded hard deadline", logger.Data{"deadline": shutdownHardDeadline.String()})
		os.Exit(1)
	})
	defer watchdog.Stop()

	shutdownCtx, cancel := context.WithTimeout(ctx, serverShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Err(errorstack.Capture(err)).Error("server shutdown error")
		if closeErr := srv.Close(); closeErr != nil {
			log.Err(errorstack.Capture(closeErr)).Error("server force-close error")
		}
	}

	log.Info("server stopped")
	return nil
}
