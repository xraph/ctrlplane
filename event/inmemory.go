package event

import (
	"context"
	"slices"
	"sync"
)

const defaultHistoryCapacity = 1000

// InMemoryBus is a channel-based event bus for single-process use and testing.
type InMemoryBus struct {
	mu      sync.RWMutex
	subs    []inMemorySub
	history []*Event
}

type inMemorySub struct {
	id      int
	handler Handler
	types   []Type
}

// NewInMemoryBus creates a new in-memory event bus.
func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{
		history: make([]*Event, 0, defaultHistoryCapacity),
	}
}

// Publish sends an event to all matching subscribers synchronously.
func (b *InMemoryBus) Publish(ctx context.Context, evt *Event) error {
	b.mu.Lock()
	b.appendHistory(evt)
	subs := make([]inMemorySub, len(b.subs))
	copy(subs, b.subs)
	b.mu.Unlock()

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

// RecentEvents returns the most recent events, optionally filtered by type.
func (b *InMemoryBus) RecentEvents(limit int, types ...Type) []*Event {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	// Iterate backward through history to collect most recent first.
	result := make([]*Event, 0, limit)

	for i := len(b.history) - 1; i >= 0 && len(result) < limit; i-- {
		evt := b.history[i]

		if len(types) > 0 && !slices.Contains(types, evt.Type) {
			continue
		}

		result = append(result, evt)
	}

	return result
}

// Close is a no-op for the in-memory bus.
func (b *InMemoryBus) Close() error {
	return nil
}

// appendHistory adds an event to the ring buffer, evicting the oldest if at capacity.
// Caller must hold b.mu write lock.
func (b *InMemoryBus) appendHistory(evt *Event) {
	if len(b.history) >= defaultHistoryCapacity {
		// Shift left by one, discarding the oldest event.
		copy(b.history, b.history[1:])
		b.history[len(b.history)-1] = evt
	} else {
		b.history = append(b.history, evt)
	}
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
