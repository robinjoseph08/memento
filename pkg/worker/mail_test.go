package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/robinjoseph08/memento/pkg/models"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type noContent struct{}

func (noContent) VisibleEntries(context.Context, bun.IDB, string) ([]notifications.VisibleEntry, error) {
	return nil, nil
}

var invitation = notifications.Message{Kind: "invitation", To: "alex@example.test", Subject: "You're invited", Body: "Sign in"}

// mailRuntime wires the Notifications module to the runtime the way main does,
// with the delivery record staying authoritative over River state.
func mailRuntime(t *testing.T, db *bun.DB, mailer notifications.Mailer, concurrency int) (*notifications.Module, *Runtime) {
	t.Helper()
	var module *notifications.Module
	runtime, err := New(db, func(context.Context, string) error { return nil }, Mail(func(ctx context.Context, id string) error {
		return module.Execute(ctx, id, FinalAttempt(ctx))
	}, concurrency))
	require.NoError(t, err)
	module = notifications.New(db, mailer, runtime.EnqueueMail, noContent{}, nil)
	return module, runtime
}

func enqueueMail(t *testing.T, ctx context.Context, db *bun.DB, module *notifications.Module) notifications.Delivery {
	t.Helper()
	var delivery notifications.Delivery
	require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		delivery, err = module.Enqueue(ctx, tx, invitation)
		return err
	}))
	return delivery
}

func deliveryStatus(t *testing.T, ctx context.Context, db *bun.DB, module *notifications.Module, id string) notifications.Delivery {
	t.Helper()
	deliveries, err := module.Deliveries(ctx, db, []string{id})
	require.NoError(t, err)
	return deliveries[id]
}

func TestEnqueueMailIsTransactionalDeduplicatedAndIsolated(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	module, runtime := mailRuntime(t, db, &notifications.Recorder{}, 0)
	rollback := errors.New("rollback")
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := module.Enqueue(ctx, tx, invitation); err != nil {
			return err
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	assertJobCount(t, ctx, db, 0)
	records, err := db.NewSelect().Model((*models.MailDelivery)(nil)).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, records, "delivery intent and durable work roll back together")

	delivery := enqueueMail(t, ctx, db, module)
	var row struct {
		Args        string
		MaxAttempts int
		Queue       string
		Kind        string
	}
	require.NoError(t, db.NewSelect().Table("river_job").Column("args", "max_attempts", "queue", "kind").Scan(ctx, &row))
	assert.JSONEq(t, `{"delivery_id":"`+delivery.ID+`"}`, row.Args)
	assert.Equal(t, 5, row.MaxAttempts)
	assert.Equal(t, "mail", row.Queue)
	assert.Equal(t, "deliver_mail", row.Kind)
	enqueue(t, ctx, db, runtime, "album")
	var queues []string
	require.NoError(t, db.NewSelect().Table("river_job").Column("queue").Order("queue").Scan(ctx, &queues))
	assert.Equal(t, []string{"imports", "mail"}, queues, "mail never shares the import queue")

	// Repeated retry clicks for one delivery add no work while it is active:
	// the delivery record, not River uniqueness, is the guard.
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			results <- db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				_, err := module.Retry(ctx, tx, delivery.ID)
				return err
			})
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	assertJobCount(t, ctx, db, 2)

	importOnly, err := New(db, func(context.Context, string) error { return nil })
	require.NoError(t, err)
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error { return importOnly.EnqueueMail(ctx, tx, delivery.ID) })
	require.Error(t, err, "a runtime without a mail executor refuses mail work")
}

func TestMailQueueBoundsConcurrencyAndCompletesDeliveries(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	var active, peak atomic.Int32
	release := make(chan struct{})
	recorder := &notifications.Recorder{AfterSend: func(ctx context.Context, _ notifications.Message) error {
		current := active.Add(1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		defer active.Add(-1)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return &notifications.DeliveryError{Outcome: notifications.OutcomeUncertain, Summary: "cancelled"}
		}
	}}
	module, runtime := mailRuntime(t, db, recorder, 2)
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = enqueueMail(t, ctx, db, module).ID
	}
	completed, unsubscribe := runtime.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	// Wait until the queue is saturated, then let everything finish.
	for active.Load() < 2 {
		select {
		case <-ctx.Done():
			t.Fatal("mail workers never started")
		case <-time.After(5 * time.Millisecond):
		}
	}
	sending, err := db.NewSelect().Model((*models.MailDelivery)(nil)).Where("status = 'sending'").Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, sending)
	close(release)
	for range ids {
		receive(t, ctx, completed)
	}
	assert.LessOrEqual(t, peak.Load(), int32(2), "concurrency stays within the configured bound")
	assert.Len(t, recorder.Sent(), 5)
	for _, id := range ids {
		result := deliveryStatus(t, ctx, db, module, id)
		assert.Equal(t, "delivered", result.Status, id)
		assert.Equal(t, 1, result.Attempts)
	}
}

func TestShutdownAfterAcceptanceIsUncertainAndRestartDoesNotResend(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	accepted := make(chan struct{}, 1)
	recorder := &notifications.Recorder{AfterSend: func(ctx context.Context, _ notifications.Message) error {
		accepted <- struct{}{}
		// The SMTP adapter reports a lost connection after DATA the same way.
		<-ctx.Done()
		return &notifications.DeliveryError{Outcome: notifications.OutcomeUncertain, Summary: "The connection was lost after the message was sent, so the mail server may have accepted it."}
	}}
	module, runtime := mailRuntime(t, db, recorder, 0)
	delivery := enqueueMail(t, ctx, db, module)
	require.NoError(t, runtime.Start(ctx))
	receive(t, ctx, accepted)
	stopCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, runtime.client.StopAndCancel(stopCtx))
	uncertain := deliveryStatus(t, ctx, db, module, delivery.ID)
	assert.Equal(t, "uncertain", uncertain.Status)
	assert.Contains(t, uncertain.Message, "may have accepted")

	// A fresh process finds no automatic work for the ambiguous attempt.
	recorder.AfterSend = nil
	fresh := &notifications.Recorder{}
	freshModule, freshRuntime := mailRuntime(t, db, fresh, 0)
	completed, unsubscribe := freshRuntime.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, freshRuntime)
	var pending int
	require.NoError(t, db.NewSelect().Table("river_job").ColumnExpr("count(*)").Where("state IN ('available', 'retryable', 'scheduled', 'running')").Scan(ctx, &pending))
	assert.Zero(t, pending)
	assert.Equal(t, "uncertain", deliveryStatus(t, ctx, db, freshModule, delivery.ID).Status)
	assert.Empty(t, fresh.Sent())

	// Only a deliberate retry sends again.
	require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := freshModule.Retry(ctx, tx, delivery.ID)
		return err
	}))
	event := receive(t, ctx, completed)
	assert.Equal(t, rivertype.JobStateCompleted, event.Job.State)
	delivered := deliveryStatus(t, ctx, db, freshModule, delivery.ID)
	assert.Equal(t, "delivered", delivered.Status)
	assert.Equal(t, 2, delivered.Attempts)
	assert.Len(t, fresh.Sent(), 1)
	assert.Len(t, recorder.Sent(), 1)
}

func TestRetryAfterCrashRunsDespiteOrphanedRunningJob(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	recorder := &notifications.Recorder{}
	module, runtime := mailRuntime(t, db, recorder, 0)
	delivery := enqueueMail(t, ctx, db, module)
	// A crashed process left its River job running and the record sending.
	_, err := db.NewUpdate().Table("river_job").Set("state = 'running', attempt = 1, attempted_at = CURRENT_TIMESTAMP").Where("true").Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*models.MailDelivery)(nil)).Set("status = 'sending', attempts = 1").Where("id = ?", delivery.ID).Exec(ctx)
	require.NoError(t, err)
	recovered, err := module.RecoverInterrupted(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := module.Retry(ctx, tx, delivery.ID)
		return err
	}))
	assertJobCount(t, ctx, db, 2)
	completed, unsubscribe := runtime.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	receive(t, ctx, completed)
	result := deliveryStatus(t, ctx, db, module, delivery.ID)
	assert.Equal(t, "delivered", result.Status)
	assert.Equal(t, 2, result.Attempts)
	assert.Len(t, recorder.Sent(), 1, "the deliberate retry runs without waiting for River's rescuer")
}

func TestRescuedJobAfterProcessDeathMarksDeliveryUncertain(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	recorder := &notifications.Recorder{}
	module, runtime := mailRuntime(t, db, recorder, 0)
	delivery := enqueueMail(t, ctx, db, module)
	// A dead process claimed the record and River later rescued its job.
	_, err := db.NewUpdate().Model((*models.MailDelivery)(nil)).Set("status = 'sending', attempts = 1").Where("id = ?", delivery.ID).Exec(ctx)
	require.NoError(t, err)
	completed, unsubscribe := runtime.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	receive(t, ctx, completed)
	result := deliveryStatus(t, ctx, db, module, delivery.ID)
	assert.Equal(t, "uncertain", result.Status)
	assert.Contains(t, result.Message, "restarted")
	assert.Empty(t, recorder.Sent(), "an ambiguous attempt is never resent automatically")
}

type staticContent map[string][]notifications.VisibleEntry

func (c staticContent) VisibleEntries(_ context.Context, _ bun.IDB, personID string) ([]notifications.VisibleEntry, error) {
	return c[personID], nil
}

// TestApprovedUpdateEmailRunsOnTheMailQueue approves an Update Notification
// for a subscribed Person and lets the runtime deliver its email: the job
// commits with the approval on the mail queue, and the delivery record, not
// River, reports the outcome.
func TestApprovedUpdateEmailRunsOnTheMailQueue(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	ctx := deadline(t)
	now := time.Now().UTC()
	album := models.Album{ID: models.NewUUIDv7(), SourceID: "source", Title: "Coast", ImportStatus: "complete", ImportUpdatedAt: now, CreatedAt: now}
	_, err := db.NewInsert().Model(&album).Exec(ctx)
	require.NoError(t, err)
	item := models.MediaItem{ID: models.NewUUIDv7(), SourceID: "asset", Checksum: "sum", Filename: "surf.mp4", Kind: "VIDEO", CapturedAt: now, SourceCreatedAt: now, SourceUpdatedAt: now, ContentVersion: "1"}
	_, err = db.NewInsert().Model(&item).Exec(ctx)
	require.NoError(t, err)
	entry := models.AlbumEntry{ID: models.NewUUIDv7(), AlbumID: album.ID, MediaItemID: item.ID}
	moment := models.Moment{ID: models.NewUUIDv7(), AlbumID: album.ID, CaptureDate: "2026-01-01", CoverEntryID: entry.ID}
	entry.MomentID = &moment.ID
	require.NoError(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(&moment).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&entry).Exec(ctx)
		return err
	}))
	person := models.Person{ID: models.NewUUIDv7(), DisplayName: "Alex", OnboardingCompletedAt: &now, CreatedAt: now}
	_, err = db.NewInsert().Model(&person).Exec(ctx)
	require.NoError(t, err)
	identity := models.Identity{ID: models.NewUUIDv7(), PersonID: person.ID, Provider: "fake", Subject: "alex", Email: "alex@example.test", CreatedAt: now}
	_, err = db.NewInsert().Model(&identity).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*models.Person)(nil)).Set("update_identity_id = ?, email_updates = true", identity.ID).Where("id = ?", person.ID).Exec(ctx)
	require.NoError(t, err)
	content := staticContent{person.ID.String(): {{AlbumID: album.ID.String(), AlbumTitle: "Coast", EntryID: entry.ID.String(), Kind: "VIDEO"}}}

	recorder := &notifications.Recorder{}
	var module *notifications.Module
	runtime, err := New(db, func(context.Context, string) error { return nil }, Mail(func(ctx context.Context, id string) error {
		return module.Execute(ctx, id, FinalAttempt(ctx))
	}, 0))
	require.NoError(t, err)
	module = notifications.New(db, recorder, runtime.EnqueueMail, content, nil)
	module.PublicURL = "https://memento.example.test"
	preview, err := module.PreviewUpdates(ctx)
	require.NoError(t, err)
	require.Len(t, preview.People, 1)
	approval, err := module.ApproveUpdates(ctx, notifications.ApproveRequest{People: []notifications.ApprovePerson{{PersonID: person.ID.String(), ReviewToken: preview.People[0].ReviewToken}}})
	require.NoError(t, err)
	require.NotNil(t, approval.People[0].Delivery)
	var job struct {
		Queue string
		Kind  string
	}
	require.NoError(t, db.NewSelect().Table("river_job").Column("queue", "kind").Scan(ctx, &job))
	assert.Equal(t, "mail", job.Queue)
	assert.Equal(t, "deliver_mail", job.Kind)

	completed, unsubscribe := runtime.client.Subscribe(river.EventKindJobCompleted)
	t.Cleanup(unsubscribe)
	startRuntime(t, ctx, runtime)
	receive(t, ctx, completed)
	delivered := deliveryStatus(t, ctx, db, module, approval.People[0].Delivery.ID)
	assert.Equal(t, "delivered", delivered.Status)
	require.Len(t, recorder.Sent(), 1)
	assert.Equal(t, "alex@example.test", recorder.Sent()[0].To)
	assert.Equal(t, "Coast was shared with you on Memento", recorder.Sent()[0].Subject)
	assert.Contains(t, recorder.Sent()[0].Body, "Coast (new album)\n1 video\n")
	assert.Contains(t, recorder.Sent()[0].Body, "/albums/"+album.ID.String()+"/photos")
	open, err := module.OpenDeliveries(ctx)
	require.NoError(t, err)
	assert.Empty(t, open)
}
