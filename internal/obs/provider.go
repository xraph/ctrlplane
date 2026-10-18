package obs

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	ctrlplane "github.com/xraph/ctrlplane"
)

// Provider operations, the values of the operation label and of the
// ctrlplane.provider.operation span attribute.
const (
	OpProvision   = "provision"
	OpDeprovision = "deprovision"
	OpStart       = "start"
	OpStop        = "stop"
	OpRestart     = "restart"
	OpScale       = "scale"
	OpDeploy      = "deploy"
	OpStatus      = "status"
	OpLogs        = "logs"
	OpResources   = "resources"
)

// providerSpanNames maps an operation to its span name.
var providerSpanNames = map[string]string{
	OpProvision:   "provision instance",
	OpDeprovision: "deprovision instance",
	OpStart:       "start instance",
	OpStop:        "stop instance",
	OpRestart:     "restart instance",
	OpScale:       "scale instance",
	OpDeploy:      "deploy instance",
	OpStatus:      "read instance status",
	OpLogs:        "read instance logs",
	OpResources:   "read instance resources",
}

// ProviderCall is one provider operation in flight. End it with the call's
// error.
type ProviderCall struct {
	m            *Metrics
	span         trace.Span
	providerType string
	op           string
	start        time.Time
}

// StartProviderCall times one provider operation and, when ctx carries a
// span, traces it as a child. providerType is the provider's Info().Name
// and op one of the Op constants. m may be nil.
//
// The returned context has no baggage. Providers call cloud and cluster
// APIs, often through instrumented clients that would forward whatever
// baggage a caller sent in, and that baggage often names users or tenants.
func (m *Metrics) StartProviderCall(ctx context.Context, providerType, op string, attrs ...attribute.KeyValue) (context.Context, *ProviderCall) {
	ctx = baggage.ContextWithoutBaggage(ctx)

	name, ok := providerSpanNames[op]
	if !ok {
		name = "call provider"
	}

	attrs = append(attrs, AttrProviderType.String(providerType), AttrProviderOperation.String(op))

	ctx, span := StartChild(ctx, name, trace.WithAttributes(attrs...))

	return ctx, &ProviderCall{m: m, span: span, providerType: providerType, op: op, start: time.Now()}
}

// End records the call's duration and result and ends its span.
func (c *ProviderCall) End(err error) {
	if c == nil {
		return
	}

	Fail(c.span, err)
	c.finish(Result(err))
}

// EndPoll ends a status read made while waiting for readiness. Right after
// a platform accepts a request it often answers "not found" for a moment,
// and the wait retries; such a read (IsNotFound) is recorded with result
// not_found and leaves the span unmarked. Any other error ends the call as
// End would.
func (c *ProviderCall) EndPoll(err error) {
	if c == nil {
		return
	}

	if err != nil && IsNotFound(err) {
		c.span.SetAttributes(AttrProviderNotFound.Bool(true))
		c.finish(ResultNotFound)

		return
	}

	c.End(err)
}

func (c *ProviderCall) finish(result string) {
	c.span.End()

	if c.m != nil {
		c.m.providerCallDuration.with(ProviderLabel(c.providerType), c.op, result).Observe(time.Since(c.start).Seconds())
	}
}

// ResultNotFound is the provider call result for a readiness status read
// that found nothing yet.
const ResultNotFound = "not_found"

// IsNotFound reports whether err says the thing asked about doesn't exist
// (yet): it wraps ctrlplane.ErrNotFound, or carries the NotFound() marker
// method errdefs-style errors use, as the Docker SDK's do. Errors that only
// spell it out in their text, or Kubernetes API errors, aren't recognised.
func IsNotFound(err error) bool {
	if errors.Is(err, ctrlplane.ErrNotFound) {
		return true
	}

	var marker interface{ NotFound() }

	return errors.As(err, &marker)
}
