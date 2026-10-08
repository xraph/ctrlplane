# Ctrl Plane

A composable Go library for deploying and managing SaaS instances at scale.

Ctrl Plane handles instance lifecycle, zero-downtime deployments, health monitoring, secret management, traffic routing, and multi-tenant isolation. You bring a cloud provider and an auth system. Ctrl Plane wires everything together.

## Overview

Ctrl Plane is a library, not a framework. You import Go packages, configure them with functional options, and embed them into your own application. It runs standalone or as a [Forge](https://github.com/xraph/forge) extension.

```go
cp, err := app.New(
    app.WithStore(postgresStore),
    app.WithProvider("k8s", kubernetesProvider),
    app.WithAuth(myAuthProvider),
)

cp.Start(ctx)
http.ListenAndServe(":8080", api.New(cp).Handler())
```

**What it does:**

- Provisions and manages tenant instances across any cloud provider (Docker, Kubernetes, AWS ECS, Fly.io, etc.)
- Deploys with rolling, blue-green, canary, or recreate strategies
- Runs HTTP, TCP, gRPC, and command-based health checks
- Manages custom domains, TLS certificates, and traffic routing
- Stores secrets with pluggable vault backends
- Publishes lifecycle events and delivers webhooks
- Collects metrics, logs, traces, and resource snapshots
- Enforces tenant quotas and records audit trails

## Install

```bash
go get github.com/xraph/ctrlplane@latest
```

Requires Go 1.26 or later.

## Quick start

A minimal server with an in-memory store and Docker provider:

```go
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "time"

    "github.com/xraph/ctrlplane/api"
    "github.com/xraph/ctrlplane/app"
    "github.com/xraph/ctrlplane/auth"
    "github.com/xraph/ctrlplane/provider/docker"
    "github.com/xraph/ctrlplane/store/memory"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    memStore := memory.New()
    dockerProv, err := docker.New(docker.Config{
        Host: "unix:///var/run/docker.sock",
    })
    if err != nil {
        log.Fatal(err)
    }

    cp, err := app.New(
        app.WithStore(memStore),
        app.WithAuth(auth.NewNoopProvider()),
        app.WithProvider("docker", dockerProv),
        app.WithDefaultProvider("docker"),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := cp.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer cp.Stop(context.Background())

    srv := &http.Server{
        Addr:              ":8080",
        Handler:           api.New(cp).Handler(),
        ReadHeaderTimeout: 10 * time.Second,
    }

    go func() {
        <-ctx.Done()
        srv.Shutdown(context.Background())
    }()

    log.Println("ctrlplane listening on :8080")
    if err := srv.ListenAndServe(); err != http.ErrServerClosed {
        log.Fatal(err)
    }
}
```

Create a tenant:

```bash
curl -X POST http://localhost:8080/v1/admin/tenants \
  -H "Content-Type: application/json" \
  -d '{"name": "Acme Corp", "slug": "acme", "plan": "pro"}'
```

Create an instance:

```bash
curl -X POST http://localhost:8080/v1/instances \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "ten_...",
    "name": "web-app",
    "image": "nginx:alpine",
    "provider_name": "docker",
    "resources": {"cpu_millis": 500, "memory_mb": 256},
    "ports": [{"container_port": 80, "protocol": "tcp"}]
  }'
```

## React dashboard

Register the Ctrlplane extension before the Forge dashboard extension, then add `@forge-go/dashboard-plugin-ctrlplane` to your React shell. You can find the plugin and persistent local demo in `forge-dashboard/packages/plugin-ctrlplane` in the sibling dashboard checkout. The app mounts at `/@ctrlplane` and exposes workloads, replicas, deployments, templates, health, networking and administration through typed contract intents.

Your dashboard authentication must supply a trusted subject, `ctrlplane:read` or `ctrlplane:write`, and a nonempty `tenant_id` claim. Global administration requires `system:admin`. Every intent also checks the configured Ctrlplane authorization provider. The old templ contributor and its injected administrator claims have been removed.

See [the migration record](MIGRATION.md) for the legacy inventory, behavior changes, verification evidence and remaining infrastructure limits. The local demo persists domain records in Badger and simulates provider operations; it does not start containers or provision cloud resources.

## Package structure

```
ctrlplane.go          Root package (Entity, Config, sentinel errors)
id/                      TypeID-based identifiers (prefix-qualified, UUIDv7)
auth/                    Authentication and authorization interface
instance/                Instance lifecycle management
deploy/                  Deployments, releases, and strategies
health/                  Health checks (HTTP, TCP, gRPC, command)
network/                 Domains, routes, TLS certificates
secrets/                 Secret management with pluggable vault
telemetry/               Metrics, logs, traces, resource snapshots
admin/                   Tenant management, quotas, audit
event/                   Event bus and webhooks
worker/                  Background task scheduler
provider/                Cloud provider abstraction
  docker/                Docker provider (implemented)
  kubernetes/            Kubernetes (interface defined)
  aws/, gcp/, azure/     Cloud providers (interface defined)
  nomad/, fly/           Additional providers (interface defined)
store/                   Persistence layer
  memory/                In-memory (tests and development)
  sqlite/                SQLite (standalone deployments)
  postgres/              PostgreSQL (production)
api/                     HTTP handlers and middleware
app/                     Root orchestrator (wires everything together)
extension/               Forge extension adapter
cmd/ctrlplane/        Reference binary
```

## Key interfaces

Every subsystem is defined by Go interfaces. Swap out any piece with your own implementation.

| Interface | Package | Purpose |
|-----------|---------|---------|
| `provider.Provider` | `provider/` | Cloud orchestrator abstraction |
| `instance.Service` | `instance/` | Instance CRUD and lifecycle |
| `deploy.Service` | `deploy/` | Deployment orchestration |
| `deploy.Strategy` | `deploy/` | Pluggable deployment strategy |
| `health.Checker` | `health/` | Health check implementation |
| `network.Router` | `network/` | Traffic routing abstraction |
| `secrets.Vault` | `secrets/` | Backend secret storage |
| `telemetry.Collector` | `telemetry/` | Custom telemetry source |
| `event.Bus` | `event/` | Event publish/subscribe |
| `auth.Provider` | `auth/` | Authentication and authorization |
| `store.Store` | `store/` | Aggregate persistence |

## Forge extension

Mount Ctrl Plane into a Forge application:

```go
app := forge.New()
app.Use(cpext.New(
    cpext.WithProvider("k8s", kubernetesProvider),
    cpext.WithAuthProvider(authsomeAdapter),
))
app.Run()
```

## Documentation

Full documentation is available in the `docs/` directory and covers architecture, core concepts, every subsystem, guides for writing custom providers and deployment strategies, and the complete HTTP API reference.

## Development

```bash
go build ./...
go test ./...
golangci-lint run ./...
goimports -w -local github.com/xraph/ctrlplane .
```

## License

See [LICENSE](LICENSE) for details.
