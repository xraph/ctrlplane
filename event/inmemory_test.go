package event_test

import (
	"context"
	"testing"

	"github.com/xraph/ctrlplane/event"
)

func TestInMemoryBus_PublishSubscribe(t *testing.T) {
	t.Parallel()

	bus := event.NewInMemoryBus()
	defer bus.Close()

	var received *event.Event

	bus.Subscribe(func(_ context.Context, evt *event.Event) error {
		received = evt

		return nil
	})

	evt := event.NewEvent(event.InstanceCreated, "tenant-1")
	if err := bus.Publish(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if received == nil {
		t.Fatal("handler was not called")
	}

	if received.Type != event.InstanceCreated {
		t.Errorf("expected type %s, got %s", event.InstanceCreated, received.Type)
	}
}

func TestInMemoryBus_FilteredSubscription(t *testing.T) {
	t.Parallel()

	bus := event.NewInMemoryBus()
	defer bus.Close()

	callCount := 0

	bus.Subscribe(func(_ context.Context, _ *event.Event) error {
		callCount++

		return nil
	}, event.InstanceCreated, event.InstanceDeleted)

	ctx := context.Background()

	_ = bus.Publish(ctx, event.NewEvent(event.InstanceCreated, "t1"))
	_ = bus.Publish(ctx, event.NewEvent(event.InstanceStarted, "t1"))
	_ = bus.Publish(ctx, event.NewEvent(event.InstanceDeleted, "t1"))
	_ = bus.Publish(ctx, event.NewEvent(event.DeployStarted, "t1"))

	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestInMemoryBus_Unsubscribe(t *testing.T) {
	t.Parallel()

	bus := event.NewInMemoryBus()
	defer bus.Close()

	callCount := 0

	sub := bus.Subscribe(func(_ context.Context, _ *event.Event) error {
		callCount++

		return nil
	})

	ctx := context.Background()
	_ = bus.Publish(ctx, event.NewEvent(event.InstanceCreated, "t1"))

	sub.Unsubscribe()

	_ = bus.Publish(ctx, event.NewEvent(event.InstanceCreated, "t1"))

	if callCount != 1 {
		t.Errorf("expected 1 call after unsubscribe, got %d", callCount)
	}
}

func TestInMemoryBus_AllEventsSubscription(t *testing.T) {
	t.Parallel()

	bus := event.NewInMemoryBus()
	defer bus.Close()

	callCount := 0

	bus.Subscribe(func(_ context.Context, _ *event.Event) error {
		callCount++

		return nil
	})

	ctx := context.Background()
	_ = bus.Publish(ctx, event.NewEvent(event.InstanceCreated, "t1"))
	_ = bus.Publish(ctx, event.NewEvent(event.DeployStarted, "t1"))
	_ = bus.Publish(ctx, event.NewEvent(event.HealthCheckPassed, "t1"))

	if callCount != 3 {
		t.Errorf("expected 3 calls for all-events subscriber, got %d", callCount)
	}
}
