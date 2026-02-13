package api

import (
	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/telemetry"
)

// queryMetrics handles GET /v1/instances/:instanceID/metrics.
func (a *API) queryMetrics(ctx forge.Context, req *InstanceTelemetryRequest) ([]telemetry.Metric, error) {
	q := telemetry.MetricQuery{
		InstanceID: req.InstanceID,
	}

	metrics, err := a.cp.Telemetry.QueryMetrics(ctx.Context(), q)
	if err != nil {
		return nil, mapError(err)
	}

	return metrics, nil
}

// queryLogs handles GET /v1/instances/:instanceID/logs.
func (a *API) queryLogs(ctx forge.Context, req *InstanceTelemetryRequest) ([]telemetry.LogEntry, error) {
	q := telemetry.LogQuery{
		InstanceID: req.InstanceID,
	}

	logs, err := a.cp.Telemetry.QueryLogs(ctx.Context(), q)
	if err != nil {
		return nil, mapError(err)
	}

	return logs, nil
}

// queryTraces handles GET /v1/instances/:instanceID/traces.
func (a *API) queryTraces(ctx forge.Context, req *InstanceTelemetryRequest) ([]telemetry.Trace, error) {
	q := telemetry.TraceQuery{
		InstanceID: req.InstanceID,
	}

	traces, err := a.cp.Telemetry.QueryTraces(ctx.Context(), q)
	if err != nil {
		return nil, mapError(err)
	}

	return traces, nil
}

// getResources handles GET /v1/instances/:instanceID/resources.
func (a *API) getResources(ctx forge.Context, req *InstanceTelemetryRequest) (*telemetry.ResourceSnapshot, error) {
	snap, err := a.cp.Telemetry.GetCurrentResources(ctx.Context(), req.InstanceID)
	if err != nil {
		return nil, mapError(err)
	}

	return snap, nil
}

// getDashboard handles GET /v1/instances/:instanceID/dashboard.
func (a *API) getDashboard(ctx forge.Context, req *InstanceTelemetryRequest) (*telemetry.DashboardData, error) {
	dash, err := a.cp.Telemetry.GetDashboard(ctx.Context(), req.InstanceID)
	if err != nil {
		return nil, mapError(err)
	}

	return dash, nil
}
