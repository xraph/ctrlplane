package worker

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/network"
)

// CertRenewer periodically checks for expiring TLS certificates and renews them.
type CertRenewer struct {
	network  network.Service
	events   event.Bus
	interval time.Duration
}

// NewCertRenewer creates a new certificate renewer worker.
func NewCertRenewer(network network.Service, events event.Bus, interval time.Duration) *CertRenewer {
	return &CertRenewer{
		network:  network,
		events:   events,
		interval: interval,
	}
}

// Name returns the worker name.
func (c *CertRenewer) Name() string {
	return "cert_renewer"
}

// Interval returns how often the certificate renewer should run.
func (c *CertRenewer) Interval() time.Duration {
	return c.interval
}

// Run executes one certificate renewal cycle.
// TODO: implement certificate renewal. List all certificates nearing expiry,
// attempt renewal via the network service, and publish events for successful
// renewals or failures.
func (c *CertRenewer) Run(_ context.Context) error {
	// TODO: implement
	return nil
}
