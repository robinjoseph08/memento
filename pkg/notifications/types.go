package notifications

import "time"

// Delivery is the Curator-facing state of one email. Status is one of queued,
// sending, delivered, failed, or uncertain.
type Delivery struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	Message     string     `json:"message"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
}

// Baseline counts the content announced to a Person so far.
type Baseline struct {
	Albums  int `json:"albums"`
	Entries int `json:"entries"`
}
