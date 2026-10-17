// Package obs holds the tracing and metrics plumbing every Ctrl Plane
// subsystem shares: the tracer, the span helpers that keep error details
// off spans, and the labelled metric families.
//
// Spans go to the global OpenTelemetry tracer provider, which forge.New
// installs. Library level work (provider calls, deployments, renders,
// managed transactions) only starts a span when the context already
// carries one, so a background poller that has no trace of its own never
// mints a root trace per tick. The background workers start their own
// root spans, with trace.WithNewRoot, once they know there is work.
//
// Metrics are recorded through a go-utils metrics sink. In a Forge app that
// is app.Metrics(), which the ctrlplane extension passes in. A nil sink
// records nothing.
package obs

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/managed"
)

// InstrumentationName is the instrumentation scope on every span Ctrl
// Plane starts.
const InstrumentationName = "github.com/xraph/ctrlplane"

// Tracer returns the Ctrl Plane tracer from the global provider. It is
// looked up on each call so a provider installed after construction, as
// forge.New does, still receives the spans.
func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationName)
}

// Start starts a span under whatever span ctx carries, or a root span when
// it carries none. End it when the work is done.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, opts...) //nolint:spancheck // the caller ends the span
}

// StartRoot starts a span that begins a new trace, whatever ctx carries.
// Background workers use it for one unit of work so the span never hangs
// off the boot span of the context the worker was started with.
func StartRoot(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	opts = append(opts, trace.WithNewRoot())

	return Tracer().Start(ctx, name, opts...) //nolint:spancheck // the caller ends the span
}

// Detach returns ctx without the span it carries, keeping its values and
// cancellation. Long-lived goroutines started from a lifecycle hook use it:
// Forge hands Start a context holding the boot span, and anything traced
// under it later would join the boot trace.
func Detach(ctx context.Context) context.Context {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}

	return trace.ContextWithSpanContext(ctx, trace.SpanContext{})
}

// StartChild starts a span only when ctx already carries a valid one.
// Otherwise it returns ctx unchanged and the non-recording span ctx holds,
// so the caller can use the result the same way either way.
func StartChild(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx, trace.SpanFromContext(ctx)
	}

	return Tracer().Start(ctx, name, opts...) //nolint:spancheck // the caller ends the span
}

// Fail marks span failed. The status description and the exception event
// carry only the error's class (see ErrorType), never its message: driver
// and provider messages quote names, addresses and payload fragments.
//
// extra names package specific sentinels for ErrorType.
func Fail(span trace.Span, err error, extra ...Sentinel) {
	if err == nil || !span.IsRecording() {
		return
	}

	FailClass(span, ErrorType(err, extra...))
}

// FailClass marks span failed with class as both the status description
// and the exception type. Use it for failures that are outcomes rather
// than Go errors, such as an unhealthy probe.
func FailClass(span trace.Span, class string) {
	if !span.IsRecording() {
		return
	}

	span.SetStatus(codes.Error, class)
	span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", class)))
}

// Sentinel names an error value ErrorType should report by name instead of
// by its Go type.
type Sentinel struct {
	Err  error
	Name string
}

// sentinels are the shared errors every subsystem returns. errors.New
// values all have the Go type *errors.errorString, so naming them is the
// only way to tell them apart.
var sentinels = []Sentinel{
	{context.Canceled, "context.Canceled"},
	{context.DeadlineExceeded, "context.DeadlineExceeded"},
	{ctrlplane.ErrNotFound, "ctrlplane.ErrNotFound"},
	{ctrlplane.ErrAlreadyExists, "ctrlplane.ErrAlreadyExists"},
	{ctrlplane.ErrInvalidState, "ctrlplane.ErrInvalidState"},
	{ctrlplane.ErrProviderNotFound, "ctrlplane.ErrProviderNotFound"},
	{ctrlplane.ErrUnauthorized, "ctrlplane.ErrUnauthorized"},
	{ctrlplane.ErrForbidden, "ctrlplane.ErrForbidden"},
	{ctrlplane.ErrQuotaExceeded, "ctrlplane.ErrQuotaExceeded"},
	{ctrlplane.ErrProviderUnavail, "ctrlplane.ErrProviderUnavail"},
	{ctrlplane.ErrDeploymentFailed, "ctrlplane.ErrDeploymentFailed"},
	{ctrlplane.ErrHealthCheckFailed, "ctrlplane.ErrHealthCheckFailed"},
	{ctrlplane.ErrRollbackFailed, "ctrlplane.ErrRollbackFailed"},
	{ctrlplane.ErrInvalidConfig, "ctrlplane.ErrInvalidConfig"},
	{ctrlplane.ErrDatacenterUnavailable, "ctrlplane.ErrDatacenterUnavailable"},
	{ctrlplane.ErrNotImplemented, "ctrlplane.ErrNotImplemented"},
	{ctrlplane.ErrInvalidSource, "ctrlplane.ErrInvalidSource"},
	{ctrlplane.ErrUnsupportedSource, "ctrlplane.ErrUnsupportedSource"},
	{managed.ErrNotFound, "managed.ErrNotFound"},
	{managed.ErrConflict, "managed.ErrConflict"},
	{managed.ErrInvalid, "managed.ErrInvalid"},
	{managed.ErrBlocked, "managed.ErrBlocked"},
	{managed.ErrUnavailable, "managed.ErrUnavailable"},
	{managed.ErrUnsupported, "managed.ErrUnsupported"},
	{managed.ErrAuthority, "managed.ErrAuthority"},
	{managed.ErrCapacity, "managed.ErrCapacity"},
}

// ErrorType returns a short, value free class for err: the name of a known
// sentinel it wraps (the shared ones, then any in extra), or else the Go
// type of the innermost error in its chain. It is safe on a span and as a
// span status description. It returns "" for a nil err.
func ErrorType(err error, extra ...Sentinel) string {
	if err == nil {
		return ""
	}

	for _, s := range extra {
		if errors.Is(err, s.Err) {
			return s.Name
		}
	}

	for _, s := range sentinels {
		if errors.Is(err, s.Err) {
			return s.Name
		}
	}

	return fmt.Sprintf("%T", innermost(err))
}

// innermost follows the Unwrap chain to its end. A joined error follows its
// first branch.
func innermost(err error) error {
	for range 64 {
		switch u := err.(type) { //nolint:errorlint // walking the chain by hand is the point
		case interface{ Unwrap() error }:
			next := u.Unwrap()
			if next == nil {
				return err
			}

			err = next
		case interface{ Unwrap() []error }:
			list := u.Unwrap()
			if len(list) == 0 || list[0] == nil {
				return err
			}

			err = list[0]
		default:
			return err
		}
	}

	return err
}

// Result is the result label for err: "ok" or "error".
func Result(err error) string {
	if err != nil {
		return ResultError
	}

	return ResultOK
}

// Values of the result label.
const (
	ResultOK    = "ok"
	ResultError = "error"
)
