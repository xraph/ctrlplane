package event

import (
	"context"
	"slices"
	"sync"
)

// InMemoryBus is a channel-based event bus for single-process use and testing.
type InMemoryBus struct {
	mu   sync.RWMutex
	subs []inMemorySub
}

type inMemorySub struct {
	id      int
	handler Handler
	types   []Type
}

// NewInMemoryBus creates a new in-memory event bus.
func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{}
}

// Publish sends an event to all matching subscribers synchronously.
func (b *InMemoryBus) Publish(ctx context.Context, evt *Event) error {
	b.mu.RLock()
	subs := make([]inMemorySub, len(b.subs))
	copy(subs, b.subs)
	b.mu.RUnlock()

	for _, sub := range subs {
		if !sub.matches(evt.Type) {
			continue
		}

		if err := sub.handler(ctx, evt); err != nil {
			return err
		}
	}

	return nil
}

// Subscribe registers a handler for the given event types.
// If no types are specified, the handler receives all events.
func (b *InMemoryBus) Subscribe(handler Handler, types ...Type) Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := inMemorySub{
		id:      len(b.subs),
		handler: handler,
		types:   types,
	}
	b.subs = append(b.subs, sub)

	return &inMemorySubscription{bus: b, subID: sub.id}
}

// Close is a no-op for the in-memory bus.
func (b *InMemoryBus) Close() error {
	return nil
}

func (s *inMemorySub) matches(t Type) bool {
	if len(s.types) == 0 {
		return true
	}

	return slices.Contains(s.types, t)
}

type inMemorySubscription struct {
	bus   *InMemoryBus
	subID int
}

func (s *inMemorySubscription) Unsubscribe() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()

	s.bus.subs = slices.Delete(s.bus.subs, s.subID, s.subID+1)
}
