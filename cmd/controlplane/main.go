package main

import (
	"log"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/extension"
	"github.com/xraph/ctrlplane/provider/docker"
	"github.com/xraph/ctrlplane/store/memory"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// Create the Forge app
	forgeApp := forge.New(
		forge.WithAppName("ctrlplane"),
		forge.WithAppVersion("0.1.0"),
		forge.WithAppRouterOptions(forge.WithOpenAPI(forge.OpenAPIConfig{
			Title:       "CtrlPlane API",
			Description: "API for managing cloud infrastructure and deployments with CtrlPlane",
			Version:     "0.1.0",
			UIPath:      "/docs",
			SpecPath:    "/openapi.json",
			UIEnabled:   true,
			SpecEnabled: true,
			PrettyJSON:  true,
		})),
	)

	// Register CtrlPlane as a Forge extension
	cpExt := extension.New(
		extension.WithStore(app.WithStore(memory.New())),
		extension.WithProvider("docker", docker.New(docker.Config{})),
		extension.WithBasePath("/api/cp"),
	)

	// Use the extension with Forge
	if err := forgeApp.RegisterExtension(cpExt); err != nil {
		return err
	}

	// Run the Forge app (handles lifecycle, signals, and HTTP server)
	return forgeApp.Run()
}
