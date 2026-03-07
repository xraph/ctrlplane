package event

import "context"

// Bus is the interface for publishing and subscribing to events.
type Bus interface {
	// Publish sends an event to all matching subscribers.
	Publish(ctx context.Context, event *Event) error

	// Subscribe registers a handler for the given event types.
	// If no types are specified, the handler receives all events.
	Subscribe(handler Handler, types ...Type) Subscription

	// RecentEvents returns the most recent events, optionally filtered by type.
	// If no types are specified, all recent events are returned.
	RecentEvents(limit int, types ...Type) []*Event

	// Close shuts down the event bus and releases resources.
	Close() error
}

// Handler is a function that processes an event.
type Handler func(ctx context.Context, event *Event) error

// Subscription represents an active event subscription.
type Subscription interface {
	// Unsubscribe removes this subscription.
	Unsubscribe()
}
