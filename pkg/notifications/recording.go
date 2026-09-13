package notifications

import (
	"context"
	"sync"
)

// Recorder is the deterministic Mailer for tests and fixtures. Hooks run before
// and after the simulated acceptance so tests can pause, fail, or cancel at the
// same points where a real SMTP session can break.
type Recorder struct {
	mu   sync.Mutex
	sent []Message
	// BeforeSend runs before the message is recorded. A returned error is the send result.
	BeforeSend func(context.Context, Message) error
	// AfterSend runs after the message is recorded. A returned error is the send result.
	AfterSend func(context.Context, Message) error
}

func (r *Recorder) Send(ctx context.Context, message Message) error {
	if r.BeforeSend != nil {
		if err := r.BeforeSend(ctx, message); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.sent = append(r.sent, message)
	r.mu.Unlock()
	if r.AfterSend != nil {
		return r.AfterSend(ctx, message)
	}
	return nil
}

// Sent returns every message the server would have accepted, in order.
func (r *Recorder) Sent() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.sent...)
}
