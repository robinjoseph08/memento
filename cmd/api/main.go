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
	"github.com/robinjoseph08/web-app-template/pkg/config"
	"github.com/robinjoseph08/web-app-template/pkg/database"
	"github.com/robinjoseph08/web-app-template/pkg/migrations"
	"github.com/robinjoseph08/web-app-template/pkg/server"
	"github.com/robinjoseph08/web-app-template/pkg/version"
)

const (
	shutdownHardDeadline  = 5 * time.Second
	serverShutdownTimeout = 3 * time.Second
)

func main() {
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

	db, err := database.New(cfg)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Err(closeErr).Error("database close error")
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

	srv, err := server.New(cfg)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("server started", logger.Data{"address": srv.Addr})
		serverErrors <- srv.ListenAndServe()
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
		log.Err(err).Error("server shutdown error")
		if closeErr := srv.Close(); closeErr != nil {
			log.Err(closeErr).Error("server force-close error")
		}
	}

	log.Info("server stopped")
	return nil
}
