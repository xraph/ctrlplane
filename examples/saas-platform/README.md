# SaaS Platform Example

A complete SaaS management server built with **Ctrl Plane** and **Forge**. This single-file example demonstrates every major subsystem working together: multi-tenant management, instance lifecycle, deployments, health checks, event subscriptions, and custom platform routes.

## Prerequisites

- **Go 1.25+**
- **Docker** (optional, for the Docker provider)
- A database if using a persistent store (PostgreSQL, MongoDB, or filesystem for Badger)

## Quick Start

The fastest way to run the example uses the in-memory store (no dependencies):

```bash
go run ./examples/saas-platform
```

Open the interactive API docs at [http://localhost:8080/docs](http://localhost:8080/docs).

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CP_STORE` | `memory` | Store backend: `memory`, `bun`, `badger`, `mongo` |
| `CP_BASE_PATH` | `/api/cp` | URL prefix for all Ctrl Plane routes |
| `CP_SEED` | `true` | Create demo tenants on startup |
| `CP_DOCKER_HOST` | *(auto)* | Docker daemon address |
| `CP_DOCKER_NETWORK` | `bridge` | Docker network for containers |
| `CP_BUN_DRIVER` | `postgres` | Bun driver: `postgres` or `sqlite` |
| `CP_BUN_DSN` | — | Database connection string for Bun |
| `CP_BADGER_PATH` | `./data/badger` | Directory for Badger data files |
| `CP_MONGO_URI` | `mongodb://localhost:27017` | MongoDB connection URI |
| `CP_MONGO_DATABASE` | `ctrlplane` | MongoDB database name |

Copy `.env.example` to `.env` and adjust as needed.

## Store Backends

### Memory (default)

```bash
CP_STORE=memory go run ./examples/saas-platform
```

No setup required. Data is lost on restart.

### PostgreSQL (via Bun)

```bash
CP_STORE=bun \
CP_BUN_DSN="postgres://user:pass@localhost:5432/ctrlplane?sslmode=disable" \
go run ./examples/saas-platform
```

### SQLite (via Bun)

```bash
CP_STORE=bun \
CP_BUN_DRIVER=sqlite \
CP_BUN_DSN="file:ctrlplane.db?cache=shared&_pragma=journal_mode(WAL)" \
go run ./examples/saas-platform
```

### Badger (embedded)

```bash
CP_STORE=badger \
CP_BADGER_PATH=./data/badger \
go run ./examples/saas-platform
```

### MongoDB

```bash
CP_STORE=mongo \
CP_MONGO_URI="mongodb://localhost:27017" \
go run ./examples/saas-platform
```

## Custom Platform Endpoints

In addition to the full Ctrl Plane API (under `/api/cp`), this example adds:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/platform/status` | System stats, providers, uptime |
| `GET` | `/api/platform/health` | Lightweight health probe |
| `POST` | `/api/platform/webhooks/events` | Webhook receiver |

## API Walkthrough

After starting the server, try these commands:

### Check platform status

```bash
curl -s http://localhost:8080/api/platform/status | jq
```

### List tenants (seeded)

```bash
curl -s http://localhost:8080/api/cp/v1/admin/tenants | jq
```

### Create an instance

```bash
curl -s -X POST http://localhost:8080/api/cp/v1/instances \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-app",
    "provider": "docker",
    "config": {
      "image": "nginx:alpine",
      "cpu_millis": 500,
      "memory_mb": 256
    }
  }' | jq
```

### List instances

```bash
curl -s http://localhost:8080/api/cp/v1/instances | jq
```

### Check platform health

```bash
curl -s http://localhost:8080/api/platform/health | jq
```

### Send a webhook

```bash
curl -s -X POST http://localhost:8080/api/platform/webhooks/events \
  -H "Content-Type: application/json" \
  -d '{
    "source": "github",
    "type": "push",
    "payload": {"ref": "refs/heads/main"}
  }' | jq
```

## What This Example Demonstrates

1. **Store selection** — switch between four store backends with an environment variable.
2. **Forge integration** — OpenAPI docs, structured routing, extension lifecycle.
3. **Provider registration** — Docker provider for container orchestration.
4. **Event subscriptions** — real-time logging of instance, deploy, and health events.
5. **Custom routes** — platform-specific endpoints alongside the Ctrl Plane API.
6. **Tenant seeding** — pre-populated tenants with quotas for immediate exploration.
7. **NoopAuth** — development-friendly auth that allows all operations.

## Further Reading

- [Getting Started](../../docs/content/docs/getting-started.mdx)
- [Forge Extension Guide](../../docs/content/docs/guides/forge-extension.mdx)
- [Store Documentation](../../docs/content/docs/stores/)
- [Provider Documentation](../../docs/content/docs/providers/)
