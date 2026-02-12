package event

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// Webhook registers an external endpoint for event delivery.
type Webhook struct {
	ctrlplane.Entity

	TenantID string `db:"tenant_id" json:"tenant_id"`
	URL      string `db:"url"       json:"url"`
	Secret   string `db:"secret"    json:"-"`
	Events   []Type `db:"events"    json:"events"`
	Active   bool   `db:"active"    json:"active"`
}

// WebhookDelivery tracks delivery attempts for an event to a webhook.
type WebhookDelivery struct {
	ctrlplane.Entity

	WebhookID  id.ID      `db:"webhook_id"  json:"webhook_id"`
	EventID    id.ID      `db:"event_id"    json:"event_id"`
	StatusCode int        `db:"status_code" json:"status_code"`
	Attempts   int        `db:"attempts"    json:"attempts"`
	NextRetry  *time.Time `db:"next_retry"  json:"next_retry,omitempty"`
	Error      string     `db:"error"       json:"error,omitempty"`
}
