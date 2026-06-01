package metrics

import (
	"context"

	"github.com/xraph/ctrlplane/event"
)

// AttachToEvents wires Track / Untrack to instance lifecycle events
// so the poller starts when an instance comes up and stops when it
// goes away. Returns the bus subscription so the caller can detach
// at shutdown if needed.
//
// We track on InstanceCreated + InstanceStarted so a restart cycle
// (Stop → Start) re-attaches the poller without a fresh Create.
// Untrack on Deleted only — Stopped + Suspended keep the ring buffer
// around so the UI keeps showing the last-known sparkline even
// while the workload is paused.
func AttachToEvents(svc Service, bus event.Bus) event.Subscription {
	if svc == nil || bus == nil {
		return noopSubscription{}
	}

	handler := func(_ context.Context, ev *event.Event) error {
		if ev.InstanceID.IsNil() {
			return nil
		}

		switch ev.Type {
		case event.InstanceCreated, event.InstanceStarted, event.InstanceUnsuspended:
			svc.Track(ev.InstanceID)
		case event.InstanceDeleted:
			svc.Untrack(ev.InstanceID)
		}

		return nil
	}

	return bus.Subscribe(handler,
		event.InstanceCreated,
		event.InstanceStarted,
		event.InstanceUnsuspended,
		event.InstanceDeleted,
	)
}

type noopSubscription struct{}

func (noopSubscription) Unsubscribe() {}
