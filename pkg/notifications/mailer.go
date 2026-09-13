// Package notifications owns durable mail delivery records and announcement baselines.
package notifications

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
)

// Outcome classifies a failed send so the module can choose between automatic
// retry, a final failure, and a deliberate Curator decision.
type Outcome string

const (
	// OutcomeTransient means the server did not accept the message and a later attempt is safe.
	OutcomeTransient Outcome = "transient"
	// OutcomePermanent means the server rejected the message and retrying the same message is pointless.
	OutcomePermanent Outcome = "permanent"
	// OutcomeUncertain means the message may have been accepted; only a Curator may resend it.
	OutcomeUncertain Outcome = "uncertain"
)

// Message is one transactional email. Bodies are plain text.
type Message struct {
	Kind    string
	To      string
	Subject string
	Body    string
}

// Mailer is the adapter seam for SMTP and its recording test counterpart.
// Send returns nil when the server accepted the message and a *DeliveryError otherwise.
type Mailer interface {
	Send(ctx context.Context, message Message) error
}

// DeliveryError carries the outcome and a short operator-safe summary. The
// cause keeps provider details for logs without exposing them to Curators.
type DeliveryError struct {
	Outcome Outcome
	Summary string
	Cause   error
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("%s delivery failure: %s", e.Outcome, e.Summary)
}
func (e *DeliveryError) Unwrap() error { return e.Cause }

// classifySMTP maps an SMTP reply or network failure to an outcome. Network
// failures after the message data was sent are uncertain because the server
// may have accepted the message before the connection broke.
func classifySMTP(err error, afterData bool) *DeliveryError {
	if reply, ok := errors.AsType[*textproto.Error](err); ok {
		summary := fmt.Sprintf("The mail server replied %d.", reply.Code)
		if reply.Code >= 500 {
			return &DeliveryError{Outcome: OutcomePermanent, Summary: summary, Cause: err}
		}
		return &DeliveryError{Outcome: OutcomeTransient, Summary: summary, Cause: err}
	}
	if afterData {
		return &DeliveryError{Outcome: OutcomeUncertain, Summary: "The connection was lost after the message was sent, so the mail server may have accepted it.", Cause: err}
	}
	return &DeliveryError{Outcome: OutcomeTransient, Summary: "The mail server could not be reached.", Cause: err}
}
