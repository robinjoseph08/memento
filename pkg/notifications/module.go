package notifications

import (
	"context"
	"errors"
	"time"

	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/uptrace/bun"
)

// EnqueueDelivery records durable work in the caller's transaction. It must not send.
type EnqueueDelivery func(ctx context.Context, tx bun.Tx, deliveryID string) error

// VisibleEntry is one Album Entry a Person can currently view. Title is the
// presentation title: the Curator's video title, else the filename without
// its extension. Kind is IMAGE or VIDEO.
type VisibleEntry struct {
	AlbumID    string
	AlbumTitle string
	EntryID    string
	Kind       string
	Title      string
}

// VisibleContent is the consumer-owned view of Publishing that baselines need.
// It must apply viewer eligibility only, never a Curator's administrative bypass.
type VisibleContent interface {
	VisibleEntries(ctx context.Context, db bun.IDB, personID string) ([]VisibleEntry, error)
}

// Module owns delivery records, announcement baselines, and Update
// Notifications. A nil mailer means SMTP is not configured; every other use
// case still works.
type Module struct {
	db      *bun.DB
	mailer  Mailer
	enqueue EnqueueDelivery
	content VisibleContent
	now     func() time.Time
}

func New(db *bun.DB, mailer Mailer, enqueue EnqueueDelivery, content VisibleContent, now func() time.Time) *Module {
	if now == nil {
		now = time.Now
	}
	// Truncate to PostgreSQL's timestamptz precision so in-memory values
	// equal what is read back from the database.
	clock := func() time.Time { return now().UTC().Truncate(time.Microsecond) }
	return &Module{db: db, mailer: mailer, enqueue: enqueue, content: content, now: clock}
}

// Configured reports whether email can be sent from this installation.
func (m *Module) Configured() bool { return m.mailer != nil }

func transactionError(ctx context.Context, err error) error {
	if _, expected := errors.AsType[*errcodes.Error](err); expected {
		return err
	}
	return errorstack.CaptureContext(ctx, err)
}
