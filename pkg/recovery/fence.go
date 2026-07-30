package recovery

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const recoveryTrafficLockSQL = `hashtextextended(current_database() || ':memento:recovery-traffic', 0)`

// Acquire takes a database-wide shared traffic fence. Activation takes the
// exclusive form, so it waits for in-flight work and new work observes hold.
func (s *Service) Acquire(ctx context.Context) (func(), error) {
	connection, err := s.db.DB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open Recovery traffic fence: %w", err)
	}
	if _, err := connection.ExecContext(ctx, `SELECT pg_advisory_lock_shared(`+recoveryTrafficLockSQL+`)`); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("lock Recovery traffic fence: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = connection.ExecContext(unlockCtx, `SELECT pg_advisory_unlock_shared(`+recoveryTrafficLockSQL+`)`)
			_ = connection.Close()
		})
	}, nil
}
