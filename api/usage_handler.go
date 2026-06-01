package api

import (
	"strconv"
	"time"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/metrics"
	"github.com/xraph/ctrlplane/workload"
)

// usageRangeMaxResolution caps the requested resolution at 1 hour
// so a stupid query (`?resolution=10000h`) doesn't degenerate to a
// single bucket aggregate that's effectively useless.
const usageRangeMaxResolution = time.Hour

// getInstanceUsage returns the latest known usage sample for an
// instance. Useful for the workspace cards' "current value" row
// (the dashed line above the sparkline). Returns 200 with an empty
// body when no sample is stored yet — a fresh instance the poller
// hasn't sampled yet shouldn't 404.
//
// GET /v1/instances/:instanceId/usage.
func (a *API) getInstanceUsage(ctx forge.Context) error {
	instanceID, err := id.Parse(ctx.Param("instanceId"))
	if err != nil {
		return forge.BadRequest("invalid instanceId")
	}

	sample, _ := a.cp.Metrics.Latest(instanceID)

	return ctx.JSON(200, sample)
}

// getInstanceUsageRange returns a downsampled time-series for the
// requested window. Query params:
//
//	?range=1h|6h|24h|7d  (anything time.ParseDuration accepts)
//	?resolution=...      (optional; default = auto, ~120 buckets)
//
// GET /v1/instances/:instanceId/usage/range.
func (a *API) getInstanceUsageRange(ctx forge.Context) error {
	instanceID, err := id.Parse(ctx.Param("instanceId"))
	if err != nil {
		return forge.BadRequest("invalid instanceId")
	}

	q, err := parseUsageRange(ctx)
	if err != nil {
		return err
	}

	series, err := a.cp.Metrics.Range(ctx.Request().Context(), instanceID, q)
	if err != nil {
		return forge.InternalError(err)
	}

	return ctx.JSON(200, series)
}

// streamInstanceUsage pushes new metric samples over SSE as the
// poller produces them. Same auth + keepalive shape as the other
// streaming handlers.
//
// GET /v1/instances/:instanceId/usage/stream.
func (a *API) streamInstanceUsage(ctx forge.Context, stream forge.Stream) error {
	instanceID, err := id.Parse(ctx.Param("instanceId"))
	if err != nil {
		return forge.BadRequest("invalid instanceId")
	}

	_ = stream.SetRetry(3000)

	ch, err := a.cp.Metrics.Watch(ctx.Request().Context(), instanceID)
	if err != nil {
		return forge.InternalError(err)
	}

	keepalive := time.NewTicker(keepAliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Request().Context().Done():
			return nil
		case <-stream.Context().Done():
			return nil
		case s, ok := <-ch:
			if !ok {
				return nil
			}

			if err := stream.SendJSON("usage", s); err != nil {
				return nil
			}
		case <-keepalive.C:
			_ = stream.SendComment("ping")
		}
	}
}

// getWorkloadUsageRange returns the workload-aggregated time-series
// summed across replicas.
//
// GET /v1/workloads/:workloadId/usage/range.
func (a *API) getWorkloadUsageRange(ctx forge.Context) error {
	workloadID, err := id.Parse(ctx.Param("workloadId"))
	if err != nil {
		return forge.BadRequest("invalid workloadId")
	}

	q, err := parseUsageRange(ctx)
	if err != nil {
		return err
	}

	wq := workload.MetricsRange{Since: q.Since, Until: q.Until, Resolution: q.Resolution}

	series, err := a.cp.Workloads.RangeMetrics(ctx.Request().Context(), workloadID, wq)
	if err != nil {
		return forge.InternalError(err)
	}

	return ctx.JSON(200, series)
}

// streamWorkloadUsage pushes per-replica usage events over SSE.
// The consumer aggregates client-side (or just renders per-replica
// sparklines next to the workload total).
//
// GET /v1/workloads/:workloadId/usage/stream.
func (a *API) streamWorkloadUsage(ctx forge.Context, stream forge.Stream) error {
	workloadID, err := id.Parse(ctx.Param("workloadId"))
	if err != nil {
		return forge.BadRequest("invalid workloadId")
	}

	_ = stream.SetRetry(3000)

	ch, err := a.cp.Workloads.WatchMetrics(ctx.Request().Context(), workloadID)
	if err != nil {
		return forge.InternalError(err)
	}

	keepalive := time.NewTicker(keepAliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Request().Context().Done():
			return nil
		case <-stream.Context().Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}

			if err := stream.SendJSON("usage", ev); err != nil {
				return nil
			}
		case <-keepalive.C:
			_ = stream.SendComment("ping")
		}
	}
}

// parseUsageRange reads ?range= and ?resolution= from the request,
// returns a fully-defaulted RangeQuery, and surfaces a 400 when
// either string fails to parse.
func parseUsageRange(ctx forge.Context) (metrics.RangeQuery, error) {
	q := metrics.RangeQuery{}
	now := time.Now()
	q.Until = now

	rangeStr := ctx.Request().URL.Query().Get("range")
	if rangeStr == "" {
		rangeStr = "1h"
	}

	dur, err := time.ParseDuration(rangeStr)
	if err != nil || dur <= 0 {
		return q, forge.BadRequest("invalid range parameter")
	}

	q.Since = now.Add(-dur)

	if v := ctx.Request().URL.Query().Get("resolution"); v != "" {
		// Accept either "10s" durations or raw seconds for client
		// convenience.
		if r, err := time.ParseDuration(v); err == nil && r > 0 {
			q.Resolution = capResolution(r)
		} else if n, err := strconv.Atoi(v); err == nil && n > 0 {
			q.Resolution = capResolution(time.Duration(n) * time.Second)
		} else {
			return q, forge.BadRequest("invalid resolution parameter")
		}
	}

	return q, nil
}

func capResolution(r time.Duration) time.Duration {
	if r > usageRangeMaxResolution {
		return usageRangeMaxResolution
	}

	return r
}
