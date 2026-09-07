package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/robinjoseph08/golib/logger"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

type contextKey int

const loggingContextKey contextKey = 0

// WithLogging enables query logging for work performed with the returned
// context when database debugging is enabled.
func WithLogging(ctx context.Context) context.Context {
	return context.WithValue(ctx, loggingContextKey, true)
}

type logQueryHook struct {
	log logger.Logger
}

func (*logQueryHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

func (qh *logQueryHook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	enabled, ok := ctx.Value(loggingContextKey).(bool)
	if !ok || !enabled {
		return
	}
	qh.log.Debug(event.Query)
}

// New opens and verifies a PostgreSQL connection.
func New(ctx context.Context, cfg *config.Config) (*bun.DB, error) {
	db, err := open(cfg)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < cfg.DatabaseConnectRetryCount; attempt++ {
		err = db.PingContext(ctx)
		if err == nil {
			return db, nil
		}
		err = errorstack.CaptureContext(ctx, err)
		if attempt+1 < cfg.DatabaseConnectRetryCount {
			if waitErr := waitForRetry(ctx, cfg.DatabaseConnectRetryDelay); waitErr != nil {
				err = waitErr
				break
			}
		}
	}

	if closeErr := db.Close(); closeErr != nil {
		return nil, fmt.Errorf("connect to database: %w; close database: %w", err, errorstack.Capture(closeErr))
	}
	return nil, fmt.Errorf("connect to database: %w", err)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func open(cfg *config.Config) (*bun.DB, error) {
	connector, err := pgdriver.NewDriver().OpenConnector(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	sqldb := sql.OpenDB(connector)
	sqldb.SetMaxOpenConns(cfg.DatabaseMaxOpenConns)
	sqldb.SetMaxIdleConns(cfg.DatabaseMaxIdleConns)
	db := bun.NewDB(sqldb, pgdialect.New())

	if cfg.DatabaseDebug {
		db.AddQueryHook(&logQueryHook{log: logger.NewWithLevel("debug")})
	}

	return db, nil
}
