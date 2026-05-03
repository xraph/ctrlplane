package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/workload"
)

// keepAliveInterval is how often the SSE handler emits a comment
// (`:ping`) to keep idle connections alive through proxies that
// would otherwise close them after ~60 seconds of silence.
const keepAliveInterval = 15 * time.Second

// streamInstanceHealth is the SSE handler for
// GET /v1/instances/:instanceId/health/stream.
//
// Subscribes to health.Service.Watch for the instance and forwards
// each HealthResult as a JSON SSE event. Closes when the client
// disconnects (ctx done) or the watch channel is closed.
func (a *API) streamInstanceHealth(ctx forge.Context, stream forge.Stream) error {
	instanceID, err := id.Parse(ctx.Param("instanceId"))
	if err != nil {
		return forge.BadRequest("invalid instanceId")
	}

	_ = stream.SetRetry(3000) // 3s reconnect on disconnect

	ch, err := a.cp.Health.Watch(ctx.Request().Context(), instanceID)
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
		case r, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.SendJSON("health", r); err != nil {
				return nil
			}
		case <-keepalive.C:
			_ = stream.SendComment("ping")
		}
	}
}

// streamInstanceLogs is the SSE handler for
// GET /v1/instances/:instanceId/logs/stream.
//
// Calls instance.Service.Logs with Follow=true (configurable via
// query params) and re-emits each line from the underlying provider
// stream as a JSON SSE event. The docker provider produces one
// {ts, stream, line} JSON object per line; this handler unmarshals
// it and re-emits as a typed SSE event so consumers don't have to
// double-parse.
func (a *API) streamInstanceLogs(ctx forge.Context, stream forge.Stream) error {
	instanceID, err := id.Parse(ctx.Param("instanceId"))
	if err != nil {
		return forge.BadRequest("invalid instanceId")
	}

	opts := instance.LogsOptions{
		Follow: true,
		Tail:   100,
	}
	if v := ctx.Request().URL.Query().Get("tail"); v != "" {
		var n int
		_, _ = fmt.Sscanf(v, "%d", &n)
		if n >= 0 {
			opts.Tail = n
		}
	}
	if v := ctx.Request().URL.Query().Get("follow"); v == "false" || v == "0" {
		opts.Follow = false
	}

	_ = stream.SetRetry(3000)

	rc, err := a.cp.Instances.Logs(ctx.Request().Context(), instanceID, opts)
	if err != nil {
		return forge.InternalError(err)
	}
	defer rc.Close()

	keepalive := time.NewTicker(keepAliveInterval)
	defer keepalive.Stop()

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	lines := make(chan json.RawMessage, 64)

	go func() {
		defer close(lines)
		for scanner.Scan() {
			b := append(json.RawMessage(nil), scanner.Bytes()...)
			select {
			case lines <- b:
			case <-ctx.Request().Context().Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Request().Context().Done():
			return nil
		case <-stream.Context().Done():
			return nil
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			if err := stream.Send("log", line); err != nil {
				return nil
			}
		case <-keepalive.C:
			_ = stream.SendComment("ping")
		}
	}
}

// streamWorkloadHealth fans-in HealthEvents from every replica
// in a workload.
//
// GET /v1/workloads/:workloadId/health/stream
func (a *API) streamWorkloadHealth(ctx forge.Context, stream forge.Stream) error {
	workloadID, err := id.Parse(ctx.Param("workloadId"))
	if err != nil {
		return forge.BadRequest("invalid workloadId")
	}

	_ = stream.SetRetry(3000)

	ch, err := a.cp.Workloads.WatchHealth(ctx.Request().Context(), workloadID)
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
			if err := stream.SendJSON("health", ev); err != nil {
				return nil
			}
		case <-keepalive.C:
			_ = stream.SendComment("ping")
		}
	}
}

// streamWorkloadLogs fans-in log lines from every replica in a workload.
//
// GET /v1/workloads/:workloadId/logs/stream
func (a *API) streamWorkloadLogs(ctx forge.Context, stream forge.Stream) error {
	workloadID, err := id.Parse(ctx.Param("workloadId"))
	if err != nil {
		return forge.BadRequest("invalid workloadId")
	}

	opts := workload.LogsOptions{Follow: true, Tail: 100}
	if v := ctx.Request().URL.Query().Get("tail"); v != "" {
		var n int
		_, _ = fmt.Sscanf(v, "%d", &n)
		if n >= 0 {
			opts.Tail = n
		}
	}
	if v := ctx.Request().URL.Query().Get("follow"); v == "false" || v == "0" {
		opts.Follow = false
	}

	_ = stream.SetRetry(3000)

	ch, err := a.cp.Workloads.StreamLogs(ctx.Request().Context(), workloadID, opts)
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
			if err := stream.SendJSON("log", ev); err != nil {
				return nil
			}
		case <-keepalive.C:
			_ = stream.SendComment("ping")
		}
	}
}
