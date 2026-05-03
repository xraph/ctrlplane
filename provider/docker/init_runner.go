package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/xraph/ctrlplane/provider"
)

// runInits creates and runs every Init service to completion before
// any Main/Sidecar starts. Semantics match Kubernetes initContainers:
//
//   - Inits run sequentially in DependsOn order. Two Inits with no
//     dependency relationship still run sequentially (more
//     conservative than k8s, which runs them in declaration order
//     but still serially).
//   - Each Init must exit with status 0 within initTimeout. A
//     non-zero exit or a timeout aborts the provision and surfaces
//     the failing service's name + exit code.
//   - On any Init failure, the partial project state (network,
//     completed Inits) is left in place — Deprovision will reap it.
//   - Inits don't get host port bindings or restart policies; they
//     run-once-and-exit.
//
// When the workload has no Init services this is a no-op.
func (p *Provider) runInits(ctx context.Context, req provider.ProvisionRequest, inits []provider.ServiceSpec) error {
	if len(inits) == 0 {
		return nil
	}

	for _, svc := range inits {
		if err := p.runOneInit(ctx, req, svc); err != nil {
			return fmt.Errorf("init service %q: %w", svc.Name, err)
		}
	}

	return nil
}

// initTimeout caps how long a single Init service may take. Init
// containers are expected to be quick (schema migrations, fixture
// seeding, file extraction) — anything beyond a few minutes is a
// smell. We leave the value generous so that backfills on slow
// volumes still complete.
const initTimeout = 10 * time.Minute

// runOneInit runs a single Init service container and waits for it
// to exit. Caller is expected to remove the completed container
// (whether success or failure) so re-provisioning sees a clean
// slate; we leave the actual Remove call to Deprovision/refresh
// instead of doing it inline so operators can `docker logs` the
// last failing Init for diagnosis.
func (p *Provider) runOneInit(ctx context.Context, req provider.ProvisionRequest, svc provider.ServiceSpec) error {
	name := serviceContainerName(req.InstanceID, svc.Name)

	if err := p.removeIfExists(ctx, name); err != nil {
		return err
	}

	_ = p.pullImage(ctx, svc.Image)

	cfg, hostCfg, netCfg, err := p.buildServiceContainerConfig(req, svc)
	if err != nil {
		return err
	}

	created, err := p.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, name)
	if err != nil {
		return fmt.Errorf("create init: %w", err)
	}

	if err := p.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		_ = p.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})

		return fmt.Errorf("start init: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()

	statusCh, errCh := p.cli.ContainerWait(waitCtx, created.ID, container.WaitConditionNotRunning)

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("wait init: %w", err)
		}

	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("init exited %d", status.StatusCode)
		}

		if status.Error != nil && status.Error.Message != "" {
			return fmt.Errorf("init runtime error: %s", status.Error.Message)
		}

	case <-waitCtx.Done():
		return fmt.Errorf("init timed out after %s", initTimeout)
	}

	return nil
}
