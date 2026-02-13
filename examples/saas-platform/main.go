// Package main demonstrates a complete SaaS management platform built with
// Ctrl Plane and Forge. It shows how to:
//
//   - Configure multiple store backends (memory, bun, badger, mongo).
//   - Register a Docker infrastructure provider.
//   - Subscribe to lifecycle events for logging and notifications.
//   - Add custom platform routes alongside the Ctrl Plane API.
//   - Seed demo tenants with quotas.
//
// Run with:
//
//	go run ./examples/saas-platform
//
// See the README.md in this directory for full setup instructions.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/extension"
	"github.com/xraph/ctrlplane/provider/docker"
	"github.com/xraph/ctrlplane/store"
	"github.com/xraph/ctrlplane/store/badger"
	bunstore "github.com/xraph/ctrlplane/store/bun"
	"github.com/xraph/ctrlplane/store/memory"
	"github.com/xraph/ctrlplane/store/mongo"
)

// ---------------------------------------------------------------------------
// Configuration helpers
// ---------------------------------------------------------------------------

// envOrDefault reads an environment variable or returns the fallback value.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

// envBool reads an environment variable as a boolean.
func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}

	return b
}

// ---------------------------------------------------------------------------
// Store initialization
// ---------------------------------------------------------------------------

// initStore creates a store.Store based on the CP_STORE environment variable.
// Supported values: memory (default), bun, badger, mongo.
func initStore(storeType string) (store.Store, error) {
	switch strings.ToLower(storeType) {
	case "bun":
		return bunstore.New(bunstore.Config{
			Driver: bunstore.Driver(envOrDefault("CP_BUN_DRIVER", "postgres")),
			DSN:    envOrDefault("CP_BUN_DSN", "postgres://localhost:5432/ctrlplane?sslmode=disable"),
		})

	case "badger":
		return badger.New(badger.Config{
			Path: envOrDefault("CP_BADGER_PATH", "./data/badger"),
		})

	case "mongo":
		return mongo.New(mongo.Config{
			URI:      envOrDefault("CP_MONGO_URI", "mongodb://localhost:27017"),
			Database: envOrDefault("CP_MONGO_DATABASE", "ctrlplane"),
		})

	case "memory", "":
		return memory.New(), nil

	default:
		return nil, fmt.Errorf("unsupported store type: %s (valid: memory, bun, badger, mongo)", storeType)
	}
}

// ---------------------------------------------------------------------------
// Custom response types for platform routes
// ---------------------------------------------------------------------------

// PlatformStatus combines system stats, provider info, and runtime metadata.
type PlatformStatus struct {
	Stats     *admin.SystemStats     `json:"stats"`
	Providers []admin.ProviderStatus `json:"providers"`
	StoreType string                 `json:"store_type"`
	Uptime    string                 `json:"uptime"`
	Version   string                 `json:"version"`
}

// PlatformHealth reports store connectivity and basic health.
type PlatformHealth struct {
	Status    string `json:"status"`
	StoreType string `json:"store_type"`
	Uptime    string `json:"uptime"`
}

// WebhookPayload is the body of incoming webhook events.
type WebhookPayload struct {
	Source  string         `json:"source"            validate:"required"`
	Type    string         `json:"type"              validate:"required"`
	Payload map[string]any `json:"payload,omitempty"`
}

// WebhookResponse confirms receipt of a webhook.
type WebhookResponse struct {
	Received bool   `json:"received"`
	Source   string `json:"source"`
	Type     string `json:"type"`
}

// ---------------------------------------------------------------------------
// Event subscriptions
// ---------------------------------------------------------------------------

// subscribeEvents attaches event handlers for instance, deploy, and health
// lifecycle events. In a real SaaS platform these would push to Slack, PagerDuty,
// or a webhook dispatcher.
func subscribeEvents(events event.Bus) {
	// Instance lifecycle events.
	events.Subscribe(func(_ context.Context, evt *event.Event) error {
		slog.Info("instance event",
			"type", evt.Type,
			"tenant_id", evt.TenantID,
			"instance_id", evt.InstanceID.String(),
		)

		return nil
	},
		event.InstanceCreated,
		event.InstanceStarted,
		event.InstanceStopped,
		event.InstanceFailed,
		event.InstanceDeleted,
	)

	// Deploy lifecycle events.
	events.Subscribe(func(_ context.Context, evt *event.Event) error {
		slog.Info("deploy event",
			"type", evt.Type,
			"tenant_id", evt.TenantID,
		)

		return nil
	},
		event.DeployStarted,
		event.DeploySucceeded,
		event.DeployFailed,
		event.DeployRolledBack,
	)

	// Health events — log warnings for failures and degraded state.
	events.Subscribe(func(_ context.Context, evt *event.Event) error {
		if evt.Type == event.HealthCheckFailed || evt.Type == event.HealthDegraded {
			slog.Warn("health alert",
				"type", evt.Type,
				"tenant_id", evt.TenantID,
				"instance_id", evt.InstanceID.String(),
			)
		} else {
			slog.Info("health event",
				"type", evt.Type,
				"tenant_id", evt.TenantID,
			)
		}

		return nil
	},
		event.HealthCheckPassed,
		event.HealthCheckFailed,
		event.HealthDegraded,
		event.HealthRecovered,
	)
}

// ---------------------------------------------------------------------------
// Custom platform routes
// ---------------------------------------------------------------------------

// registerPlatformRoutes adds custom routes alongside the Ctrl Plane API.
// These demonstrate how a SaaS platform extends the base functionality.
func registerPlatformRoutes(router forge.Router, cpExt *extension.Extension, storeType string, startedAt time.Time) {
	g := router.Group("/api/platform", forge.WithGroupTags("platform"))

	// GET /api/platform/status — system dashboard.
	_ = g.GET("/status", func(_ forge.Context, _ *struct{}) (*PlatformStatus, error) {
		cp := cpExt.CtrlPlane()

		stats, err := cp.Admin.SystemStats(context.Background())
		if err != nil {
			return nil, err
		}

		providers, err := cp.Admin.ListProviders(context.Background())
		if err != nil {
			return nil, err
		}

		return &PlatformStatus{
			Stats:     stats,
			Providers: providers,
			StoreType: storeType,
			Uptime:    time.Since(startedAt).Round(time.Second).String(),
			Version:   "1.0.0",
		}, nil
	},
		forge.WithSummary("Platform status"),
		forge.WithDescription("Returns system stats, provider status, and runtime information."),
		forge.WithOperationID("platformStatus"),
	)

	// GET /api/platform/health — lightweight health probe.
	_ = g.GET("/health", func(_ forge.Context, _ *struct{}) (*PlatformHealth, error) {
		cp := cpExt.CtrlPlane()
		status := "healthy"

		if err := cp.Store().Ping(context.Background()); err != nil {
			status = "unhealthy"
		}

		return &PlatformHealth{
			Status:    status,
			StoreType: storeType,
			Uptime:    time.Since(startedAt).Round(time.Second).String(),
		}, nil
	},
		forge.WithSummary("Platform health"),
		forge.WithDescription("Lightweight health check for load balancers and monitoring."),
		forge.WithOperationID("platformHealth"),
	)

	// POST /api/platform/webhooks/events — receive external webhook events.
	_ = g.POST("/webhooks/events", func(_ forge.Context, req *WebhookPayload) (*WebhookResponse, error) {
		slog.Info("webhook received",
			"source", req.Source,
			"type", req.Type,
		)

		return &WebhookResponse{
			Received: true,
			Source:   req.Source,
			Type:     req.Type,
		}, nil
	},
		forge.WithSummary("Receive webhook events"),
		forge.WithDescription("Accepts external webhook payloads for processing."),
		forge.WithOperationID("receiveWebhook"),
	)
}

// ---------------------------------------------------------------------------
// Seed data
// ---------------------------------------------------------------------------

// seedData creates demo tenants with quotas so the API is immediately usable
// for exploration and testing.
func seedData(ctx context.Context, cp *app.CtrlPlane) {
	slog.Info("seeding demo data")

	// Tenant 1: Acme Corp (pro plan with generous quotas).
	acme, err := cp.Admin.CreateTenant(ctx, admin.CreateTenantRequest{
		Name: "Acme Corp",
		Plan: "pro",
		Quota: &admin.Quota{
			MaxInstances: 50,
			MaxCPUMillis: 100000,
			MaxMemoryMB:  204800,
			MaxDiskMB:    512000,
			MaxDomains:   20,
			MaxSecrets:   100,
		},
		Metadata: map[string]string{
			"industry": "technology",
			"tier":     "enterprise",
		},
	})
	if err != nil {
		slog.Warn("failed to seed acme tenant", "error", err)
	} else {
		slog.Info("seeded tenant",
			"name", "Acme Corp",
			"id", acme.ID.String(),
			"plan", "pro",
		)
	}

	// Tenant 2: Startup Inc (free plan with modest quotas).
	startup, err := cp.Admin.CreateTenant(ctx, admin.CreateTenantRequest{
		Name: "Startup Inc",
		Plan: "free",
		Quota: &admin.Quota{
			MaxInstances: 5,
			MaxCPUMillis: 4000,
			MaxMemoryMB:  8192,
			MaxDiskMB:    20480,
			MaxDomains:   2,
			MaxSecrets:   10,
		},
		Metadata: map[string]string{
			"industry": "saas",
			"tier":     "starter",
		},
	})
	if err != nil {
		slog.Warn("failed to seed startup tenant", "error", err)
	} else {
		slog.Info("seeded tenant",
			"name", "Startup Inc",
			"id", startup.ID.String(),
			"plan", "free",
		)
	}

	slog.Info("seed complete")
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	startedAt := time.Now()

	// -----------------------------------------------------------------------
	// 1. Read configuration from environment
	// -----------------------------------------------------------------------
	storeType := envOrDefault("CP_STORE", "memory")
	basePath := envOrDefault("CP_BASE_PATH", "/api/cp")
	seed := envBool("CP_SEED", true)
	dockerHost := envOrDefault("CP_DOCKER_HOST", "")
	dockerNetwork := envOrDefault("CP_DOCKER_NETWORK", "bridge")

	slog.Info("starting saas-platform",
		"store", storeType,
		"base_path", basePath,
		"seed", seed,
	)

	// -----------------------------------------------------------------------
	// 2. Initialize the persistence store
	// -----------------------------------------------------------------------
	dataStore, err := initStore(storeType)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}

	ctx := context.Background()

	if err := dataStore.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate store: %w", err)
	}

	if err := dataStore.Ping(ctx); err != nil {
		return fmt.Errorf("ping store: %w", err)
	}

	slog.Info("store ready", "type", storeType)

	// -----------------------------------------------------------------------
	// 3. Create the Forge application with OpenAPI documentation
	// -----------------------------------------------------------------------
	forgeApp := forge.New(
		forge.WithAppName("saas-platform"),
		forge.WithAppVersion("1.0.0"),
		forge.WithAppRouterOptions(forge.WithOpenAPI(forge.OpenAPIConfig{
			Title:       "SaaS Platform API",
			Description: "Complete SaaS management API built with Ctrl Plane and Forge",
			Version:     "1.0.0",
			UIPath:      "/docs",
			SpecPath:    "/openapi.json",
			UIEnabled:   true,
			SpecEnabled: true,
			PrettyJSON:  true,
		})),
	)

	// -----------------------------------------------------------------------
	// 4. Configure and register the Ctrl Plane extension
	// -----------------------------------------------------------------------
	cpExt := extension.New(
		extension.WithStore(app.WithStore(dataStore)),
		extension.WithProvider("docker", docker.New(docker.Config{
			Host:    dockerHost,
			Network: dockerNetwork,
		})),
		extension.WithBasePath(basePath),
		extension.WithAuthProvider(&auth.NoopProvider{
			DefaultTenantID: "default",
			DefaultClaims: &auth.Claims{
				SubjectID: "dev-admin",
				TenantID:  "default",
				Email:     "admin@saas-platform.local",
				Name:      "Dev Admin",
				Roles:     []string{"system:admin"},
			},
		}),
	)

	if err := forgeApp.RegisterExtension(cpExt); err != nil {
		return fmt.Errorf("register extension: %w", err)
	}

	// -----------------------------------------------------------------------
	// 5. Subscribe to lifecycle events
	// -----------------------------------------------------------------------
	subscribeEvents(cpExt.CtrlPlane().Events())

	// -----------------------------------------------------------------------
	// 6. Register custom platform routes
	// -----------------------------------------------------------------------
	registerPlatformRoutes(forgeApp.Router(), cpExt, storeType, startedAt)

	// -----------------------------------------------------------------------
	// 7. Seed demo data (disabled with CP_SEED=false)
	// -----------------------------------------------------------------------
	if seed {
		seedData(ctx, cpExt.CtrlPlane())
	}

	// -----------------------------------------------------------------------
	// 8. Start the server
	// -----------------------------------------------------------------------
	slog.Info("saas-platform ready",
		"docs", "http://localhost:8080/docs",
		"api", "http://localhost:8080"+basePath,
		"platform", "http://localhost:8080/api/platform/status",
	)

	return forgeApp.Run()
}
