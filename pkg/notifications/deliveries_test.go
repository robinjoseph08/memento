package notifications_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// queue records enqueued delivery IDs so tests can drive execution directly,
// the way the worker's immediate adapter would.
type queue struct {
	mu  sync.Mutex
	ids []string
}

func (q *queue) enqueue(_ context.Context, tx bun.Tx, id string) error {
	if tx.Tx == nil {
		return errors.New("enqueue requires the caller's transaction")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, id)
	return nil
}

func (q *queue) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.ids)
}

type noContent struct{}

func (noContent) VisibleEntries(context.Context, bun.IDB, string) ([]notifications.VisibleEntry, error) {
	return nil, nil
}

func enqueue(t *testing.T, db *bun.DB, module *notifications.Module, message notifications.Message) notifications.Delivery {
	t.Helper()
	var delivery notifications.Delivery
	require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		delivery, err = module.Enqueue(ctx, tx, message)
		return err
	}))
	return delivery
}

func status(t *testing.T, db *bun.DB, module *notifications.Module, id string) notifications.Delivery {
	t.Helper()
	deliveries, err := module.Deliveries(t.Context(), db, []string{id})
	require.NoError(t, err)
	require.Contains(t, deliveries, id)
	return deliveries[id]
}

var invitation = notifications.Message{Kind: "invitation", To: "alex@example.test", Subject: "You're invited", Body: "Sign in at https://memento.example.test/sign-in"}

func TestDeliveryOutcomes(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	for name, scenario := range map[string]struct {
		send     error
		final    bool
		status   string
		retried  bool
		sent     int
		contains string
	}{
		"delivered":                          {status: "delivered", sent: 1},
		"permanent failure":                  {send: &notifications.DeliveryError{Outcome: notifications.OutcomePermanent, Summary: "The mail server replied 550."}, status: "failed", sent: 1, contains: "550"},
		"transient failure retries":          {send: &notifications.DeliveryError{Outcome: notifications.OutcomeTransient, Summary: "The mail server replied 451."}, status: "queued", retried: true, sent: 1, contains: "451"},
		"transient failure on final attempt": {send: &notifications.DeliveryError{Outcome: notifications.OutcomeTransient, Summary: "The mail server replied 451."}, final: true, status: "failed", sent: 1, contains: "stopped retrying after 1 attempts"},
		"unexpected error retries":           {send: errors.New("controlled"), status: "queued", retried: true, sent: 1, contains: "could not be reached"},
		"uncertain acceptance":               {send: &notifications.DeliveryError{Outcome: notifications.OutcomeUncertain, Summary: "The connection was lost after the message was sent, so the mail server may have accepted it."}, status: "uncertain", sent: 1, contains: "may have accepted"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			recorder := &notifications.Recorder{AfterSend: func(context.Context, notifications.Message) error { return scenario.send }}
			jobs := &queue{}
			module := notifications.New(db, recorder, jobs.enqueue, noContent{}, nil)
			delivery := enqueue(t, db, module, invitation)
			assert.Equal(t, "queued", delivery.Status)
			assert.Equal(t, 1, jobs.count())
			err := module.Execute(t.Context(), delivery.ID, scenario.final)
			if scenario.retried {
				require.ErrorIs(t, err, scenario.send)
			} else {
				require.NoError(t, err)
			}
			result := status(t, db, module, delivery.ID)
			assert.Equal(t, scenario.status, result.Status)
			assert.Equal(t, 1, result.Attempts)
			assert.Contains(t, result.Message, scenario.contains)
			assert.Equal(t, result.Status == "delivered", result.DeliveredAt != nil)
			assert.Len(t, recorder.Sent(), scenario.sent)
			assert.Equal(t, "alex@example.test", recorder.Sent()[0].To)
		})
	}
}

func TestExecuteWithoutMailerFailsClearlyAndEnqueueRefuses(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	jobs := &queue{}
	module := notifications.New(db, nil, jobs.enqueue, noContent{}, nil)
	assert.False(t, module.Configured())
	err := db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := module.Enqueue(ctx, tx, invitation)
		return err
	})
	require.ErrorIs(t, err, notifications.ErrMailUnconfigured)
	assert.Zero(t, jobs.count())
	// A record queued while SMTP was configured still resolves after a restart without SMTP.
	now := time.Now().UTC()
	row := models.MailDelivery{ID: models.NewUUIDv7(), Kind: "invitation", Recipient: "alex@example.test", Subject: "Hi", Body: "Body", Status: "queued", CreatedAt: now, UpdatedAt: now}
	_, err = db.NewInsert().Model(&row).Exec(t.Context())
	require.NoError(t, err)
	require.NoError(t, module.Execute(t.Context(), row.ID.String(), false))
	result := status(t, db, module, row.ID.String())
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Message, "not configured")
	err = db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := module.Retry(ctx, tx, row.ID.String())
		return err
	})
	require.ErrorIs(t, err, notifications.ErrMailUnconfigured)
}

func TestRetryIsIdempotentAndOnlyRequeuesResolvedFailures(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	recorder := &notifications.Recorder{}
	failure := &notifications.DeliveryError{Outcome: notifications.OutcomePermanent, Summary: "The mail server replied 550."}
	recorder.AfterSend = func(context.Context, notifications.Message) error { return failure }
	jobs := &queue{}
	module := notifications.New(db, recorder, jobs.enqueue, noContent{}, nil)
	delivery := enqueue(t, db, module, invitation)
	retry := func() (notifications.Delivery, error) {
		var result notifications.Delivery
		err := db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			result, err = module.Retry(ctx, tx, delivery.ID)
			return err
		})
		return result, err
	}
	queued, err := retry()
	require.NoError(t, err)
	assert.Equal(t, "queued", queued.Status)
	assert.Equal(t, 1, jobs.count(), "retrying a queued delivery adds no work")
	require.NoError(t, module.Execute(t.Context(), delivery.ID, false))
	assert.Equal(t, "failed", status(t, db, module, delivery.ID).Status)
	const clicks = 6
	results := make([]error, clicks)
	var wg sync.WaitGroup
	for i := range clicks {
		wg.Go(func() { _, results[i] = retry() })
	}
	wg.Wait()
	for _, err := range results {
		require.NoError(t, err)
	}
	assert.Equal(t, 2, jobs.count(), "concurrent retry clicks enqueue once")
	assert.Equal(t, "queued", status(t, db, module, delivery.ID).Status)
	recorder.AfterSend = nil
	require.NoError(t, module.Execute(t.Context(), delivery.ID, false))
	delivered := status(t, db, module, delivery.ID)
	assert.Equal(t, "delivered", delivered.Status)
	assert.Equal(t, 2, delivered.Attempts)
	_, err = retry()
	require.ErrorIs(t, err, notifications.ErrAlreadyDelivered)
	require.NoError(t, module.Execute(t.Context(), delivery.ID, false), "a duplicate job for a delivered record is a no-op")
	assert.Len(t, recorder.Sent(), 2)
}

func TestInterruptedSendBecomesUncertainInsteadOfResending(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	for name, afterAcceptance := range map[string]bool{"before network activity": false, "after acceptance": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			recorder := &notifications.Recorder{}
			died := errors.New("process died")
			hook := func(ctx context.Context, _ notifications.Message) error {
				// Simulate the process dying: the outcome is never recorded.
				return died
			}
			if afterAcceptance {
				recorder.AfterSend = hook
			} else {
				recorder.BeforeSend = hook
			}
			jobs := &queue{}
			module := notifications.New(db, recorder, jobs.enqueue, noContent{}, nil)
			delivery := enqueue(t, db, module, invitation)
			// Claim the record, then abandon it the way a killed process would.
			_, err := db.NewUpdate().Model((*models.MailDelivery)(nil)).Set("status = 'sending', attempts = 1").Where("id = ?", delivery.ID).Exec(t.Context())
			require.NoError(t, err)
			require.NoError(t, module.Execute(t.Context(), delivery.ID, false))
			result := status(t, db, module, delivery.ID)
			assert.Equal(t, "uncertain", result.Status)
			assert.Contains(t, result.Message, "restarted")
			assert.Empty(t, recorder.Sent(), "an ambiguous attempt is never resent automatically")
			require.NoError(t, db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
				_, err := module.Retry(ctx, tx, delivery.ID)
				return err
			}))
			recorder.BeforeSend, recorder.AfterSend = nil, nil
			require.NoError(t, module.Execute(t.Context(), delivery.ID, false))
			assert.Equal(t, "delivered", status(t, db, module, delivery.ID).Status)
			assert.Len(t, recorder.Sent(), 1)
		})
	}
}

func TestStartupRecoveryMarksInterruptedDeliveriesUncertain(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	recorder := &notifications.Recorder{}
	module := notifications.New(db, recorder, (&queue{}).enqueue, noContent{}, nil)
	interrupted := enqueue(t, db, module, invitation)
	queued := enqueue(t, db, module, invitation)
	_, err := db.NewUpdate().Model((*models.MailDelivery)(nil)).Set("status = 'sending', attempts = 1").Where("id = ?", interrupted.ID).Exec(t.Context())
	require.NoError(t, err)
	recovered, err := module.RecoverInterrupted(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, recovered)
	assert.Equal(t, "uncertain", status(t, db, module, interrupted.ID).Status)
	assert.Equal(t, "queued", status(t, db, module, queued.ID).Status, "queued work is untouched")
	require.NoError(t, module.Execute(t.Context(), interrupted.ID, false), "the rescued job finds nothing to do")
	assert.Empty(t, recorder.Sent())
	recovered, err = module.RecoverInterrupted(t.Context())
	require.NoError(t, err)
	assert.Zero(t, recovered)
}

func TestEnqueueRollsBackWithCallerTransaction(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	jobs := &queue{}
	module := notifications.New(db, &notifications.Recorder{}, jobs.enqueue, noContent{}, nil)
	rollback := errors.New("rollback")
	err := db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := module.Enqueue(ctx, tx, invitation); err != nil {
			return err
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	count, err := db.NewSelect().Model((*models.MailDelivery)(nil)).Count(t.Context())
	require.NoError(t, err)
	assert.Zero(t, count)
	err = db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := module.Enqueue(ctx, tx, notifications.Message{Kind: "invitation", To: "not an address", Subject: "x"})
		return err
	})
	require.Error(t, err)
}
