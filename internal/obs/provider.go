package obs

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
func (m *Metrics) StartProviderCall(ctx context.Context, providerType, op string, attrs ...attribute.KeyValue) (context.Context, *ProviderCall) {
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
	c.span.End()

	if c.m != nil {
		c.m.providerCallDuration.with(ProviderLabel(c.providerType), c.op, Result(err)).Observe(time.Since(c.start).Seconds())
	}
}
