package workload

import (
	"context"
	"io"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/internal/obs/obstest"
)

// spanProbeInstances records whether each replica re-list ran under a
// valid span.
type spanProbeInstances struct {
	*fakeInstances

	traced chan bool
}

func (p *spanProbeInstances) List(ctx context.Context, opts instance.ListOptions) (*instance.ListResult, error) {
	select {
	case p.traced <- trace.SpanContextFromContext(ctx).IsValid():
	default:
	}

	return p.fakeInstances.List(ctx, opts)
}

// TestFanInsDropTheRequestSpan runs each fan-in under a context that
// carries a request span and checks the replica listing, which repeats on
// a timer for as long as the stream is open, runs with no valid parent.
func TestFanInsDropTheRequestSpan(t *testing.T) {
	obstest.Record(t)

	wid := id.New(id.PrefixWorkload)
	rep := newReplica(wid, 0)

	newProbe := func() *spanProbeInstances {
		return &spanProbeInstances{
			fakeInstances: &fakeInstances{
				listFn: func() []*instance.Instance { return []*instance.Instance{rep} },
				logsFn: func(context.Context, id.ID, instance.LogsOptions) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("")), nil
				},
			},
			traced: make(chan bool, 1),
		}
	}

	// A watch that delivers nothing and closes when its context ends, as
	// health.Service.Watch does.
	idleHealth := &fakeHealth{watchFn: func(ctx context.Context, _ id.ID) (<-chan *health.HealthResult, error) {
		ch := make(chan *health.HealthResult)

		go func() {
			<-ctx.Done()
			close(ch)
		}()

		return ch, nil
	}}

	tests := []struct {
		name string
		run  func(ctx context.Context, svc *service)
	}{
		{"logs", func(ctx context.Context, svc *service) {
			svc.fanInLogs(ctx, wid, LogsOptions{Follow: true}, make(chan *LogEvent, fanInBuffer))
		}},
		{"health", func(ctx context.Context, svc *service) {
			svc.fanInHealth(ctx, wid, make(chan *HealthEvent, fanInBuffer))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := newProbe()
			svc := &service{instances: probe, health: idleHealth}

			reqCtx, span := otel.Tracer("test").Start(adminCtxStream(), "GET /workloads/{id}/stream")
			defer span.End()

			ctx, cancel := context.WithCancel(reqCtx)
			done := make(chan struct{})

			go func() {
				tt.run(ctx, svc)
				close(done)
			}()

			traced := <-probe.traced

			cancel()
			<-done

			if traced {
				t.Fatal("the fan-in listed replicas under the request span")
			}
		})
	}
}
