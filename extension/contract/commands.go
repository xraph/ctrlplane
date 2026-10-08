package contract

import (
	"context"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/workload"
)

func registerCommands(b *bindings) {
	command(b, "instances.create", func(ctx context.Context, cp *app.CtrlPlane, in instance.CreateRequest) (any, error) {
		if err := requireTenant(ctx); err != nil {
			return nil, err
		}

		if in.Name == "" {
			return nil, badRequest("Name is required.")
		}

		return cp.Instances.Create(ctx, in)
	})
	command(b, "instances.update", func(ctx context.Context, cp *app.CtrlPlane, in updateInput[instance.UpdateRequest]) (any, error) {
		target, err := parseID(in.ID, id.PrefixInstance)
		if err != nil {
			return nil, err
		}

		return cp.Instances.Update(ctx, target, in.Request)
	})
	command(b, "instances.delete", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixInstance)
		if err != nil {
			return nil, err
		}

		return ack(cp.Instances.Delete(ctx, target))
	})
	command(b, "workloads.create", func(ctx context.Context, cp *app.CtrlPlane, in workload.CreateRequest) (any, error) {
		if err := requireTenant(ctx); err != nil {
			return nil, err
		}

		if in.Name == "" {
			return nil, badRequest("Name is required.")
		}

		return cp.Workloads.Create(ctx, in)
	})
	command(b, "workloads.update", func(ctx context.Context, cp *app.CtrlPlane, in updateInput[workload.UpdateRequest]) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		return cp.Workloads.Update(ctx, target, in.Request)
	})
	command(b, "workloads.delete", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		return ack(cp.Workloads.Delete(ctx, target))
	})
	command(b, "templates.create", func(ctx context.Context, cp *app.CtrlPlane, in template.CreateRequest) (any, error) {
		if err := requireTenant(ctx); err != nil {
			return nil, err
		}

		if in.Name == "" {
			return nil, badRequest("Name is required.")
		}

		return cp.Templates.Create(ctx, in)
	})
	command(b, "templates.update", func(ctx context.Context, cp *app.CtrlPlane, in updateInput[template.UpdateRequest]) (any, error) {
		target, err := parseID(in.ID, id.PrefixTemplate)
		if err != nil {
			return nil, err
		}

		return cp.Templates.Update(ctx, target, in.Request)
	})
	command(b, "templates.delete", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixTemplate)
		if err != nil {
			return nil, err
		}

		return ack(cp.Templates.Delete(ctx, target))
	})
	command(b, "datacenters.create", func(ctx context.Context, cp *app.CtrlPlane, in datacenter.CreateRequest) (any, error) {
		if err := requireTenant(ctx); err != nil {
			return nil, err
		}

		if in.Name == "" {
			return nil, badRequest("Name is required.")
		}

		return cp.Datacenters.Create(ctx, in)
	})
	command(b, "datacenters.update", func(ctx context.Context, cp *app.CtrlPlane, in updateInput[datacenter.UpdateRequest]) (any, error) {
		target, err := parseID(in.ID, id.PrefixDatacenter)
		if err != nil {
			return nil, err
		}

		return cp.Datacenters.Update(ctx, target, in.Request)
	})
	command(b, "datacenters.delete", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDatacenter)
		if err != nil {
			return nil, err
		}

		return ack(cp.Datacenters.Delete(ctx, target))
	})
	command(b, "instances.start", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixInstance)
		if err != nil {
			return nil, err
		}

		return ack(cp.Instances.Start(ctx, target))
	})
	command(b, "instances.stop", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixInstance)
		if err != nil {
			return nil, err
		}

		return ack(cp.Instances.Stop(ctx, target))
	})
	command(b, "instances.restart", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixInstance)
		if err != nil {
			return nil, err
		}

		return ack(cp.Instances.Restart(ctx, target))
	})
	command(b, "instances.unsuspend", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixInstance)
		if err != nil {
			return nil, err
		}

		return ack(cp.Instances.Unsuspend(ctx, target))
	})
	command(b, "instances.suspend", func(ctx context.Context, cp *app.CtrlPlane, in reasonInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixInstance)
		if err != nil {
			return nil, err
		}

		if in.Reason == "" {
			return nil, badRequest("A suspension reason is required.")
		}

		return ack(cp.Instances.Suspend(ctx, target, in.Reason))
	})
	command(b, "instances.scale", func(ctx context.Context, cp *app.CtrlPlane, in updateInput[instance.ScaleRequest]) (any, error) {
		target, err := parseID(in.ID, id.PrefixInstance)
		if err != nil {
			return nil, err
		}

		return ack(cp.Instances.Scale(ctx, target, in.Request))
	})
	command(b, "workloads.restart", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		return ack(cp.Workloads.Restart(ctx, target))
	})
	command(b, "workloads.pause", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		return ack(cp.Workloads.Pause(ctx, target))
	})
	command(b, "workloads.resume", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		return ack(cp.Workloads.Resume(ctx, target))
	})
	command(b, "workloads.scale", func(ctx context.Context, cp *app.CtrlPlane, in scaleInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		if in.Replicas < 0 {
			return nil, badRequest("Replicas must be nonnegative.")
		}

		return cp.Workloads.Scale(ctx, target, in.Replicas)
	})
	command(b, "workloads.deploy", func(ctx context.Context, cp *app.CtrlPlane, in updateInput[workload.DeployRequest]) (any, error) {
		target, err := parseID(in.ID, id.PrefixWorkload)
		if err != nil {
			return nil, err
		}

		if len(in.Request.Services) == 0 {
			return nil, badRequest("Choose at least one service to deploy.")
		}

		return cp.Workloads.Deploy(ctx, target, in.Request)
	})
	command(b, "deployments.create", func(ctx context.Context, cp *app.CtrlPlane, in deploy.DeployRequest) (any, error) {
		if _, err := ownedInstance(ctx, cp, in.InstanceID.String()); err != nil {
			return nil, err
		}

		if len(in.Services) == 0 {
			return nil, badRequest("Choose at least one service to deploy.")
		}

		return cp.Deploys.Deploy(ctx, in)
	})
	command(b, "deployments.cancel", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDeployment)
		if err != nil {
			return nil, err
		}

		return ack(cp.Deploys.Cancel(ctx, target))
	})
	command(b, "deployments.rollback", func(ctx context.Context, cp *app.CtrlPlane, in rollbackInput) (any, error) {
		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		release, err := parseID(in.ReleaseID, id.PrefixRelease)
		if err != nil {
			return nil, err
		}

		rel, err := cp.Deploys.GetRelease(ctx, release)
		if err != nil {
			return nil, err
		}

		if rel.InstanceID != target {
			return nil, badRequest("Release belongs to a different instance.")
		}

		return cp.Deploys.Rollback(ctx, target, release)
	})
	command(b, "domains.create", func(ctx context.Context, cp *app.CtrlPlane, in network.AddDomainRequest) (any, error) {
		if _, err := ownedInstance(ctx, cp, in.InstanceID.String()); err != nil {
			return nil, err
		}

		if err := requireTenant(ctx); err != nil {
			return nil, err
		}

		return cp.Network.AddDomain(ctx, in)
	})
	command(b, "routes.create", func(ctx context.Context, cp *app.CtrlPlane, in network.AddRouteRequest) (any, error) {
		if _, err := ownedInstance(ctx, cp, in.InstanceID.String()); err != nil {
			return nil, err
		}

		if err := requireTenant(ctx); err != nil {
			return nil, err
		}

		return cp.Network.AddRoute(ctx, in)
	})
	command(b, "secrets.set", func(ctx context.Context, cp *app.CtrlPlane, in secrets.SetRequest) (any, error) {
		if _, err := ownedInstance(ctx, cp, in.InstanceID.String()); err != nil {
			return nil, err
		}

		if err := requireTenant(ctx); err != nil {
			return nil, err
		}

		return cp.Secrets.Set(ctx, in)
	})
	command(b, "health.configure", func(ctx context.Context, cp *app.CtrlPlane, in health.ConfigureRequest) (any, error) {
		if _, err := ownedInstance(ctx, cp, in.InstanceID.String()); err != nil {
			return nil, err
		}

		if err := requireTenant(ctx); err != nil {
			return nil, err
		}

		return cp.Health.Configure(ctx, in)
	})
	command(b, "domains.verify", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDomain)
		if err != nil {
			return nil, err
		}

		return cp.Network.VerifyDomain(ctx, target)
	})
	command(b, "domains.delete", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDomain)
		if err != nil {
			return nil, err
		}

		return ack(cp.Network.RemoveDomain(ctx, target))
	})
	command(b, "domains.provisionCert", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDomain)
		if err != nil {
			return nil, err
		}

		return cp.Network.ProvisionCert(ctx, target)
	})
	command(b, "routes.delete", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixRoute)
		if err != nil {
			return nil, err
		}

		return ack(cp.Network.RemoveRoute(ctx, target))
	})
	command(b, "health.remove", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixHealthCheck)
		if err != nil {
			return nil, err
		}

		return ack(cp.Health.Remove(ctx, target))
	})
	command(b, "health.run", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixHealthCheck)
		if err != nil {
			return nil, err
		}

		return cp.Health.RunCheck(ctx, target)
	})
	command(b, "routes.update", func(ctx context.Context, cp *app.CtrlPlane, in updateInput[network.UpdateRouteRequest]) (any, error) {
		target, err := parseID(in.ID, id.PrefixRoute)
		if err != nil {
			return nil, err
		}

		return cp.Network.UpdateRoute(ctx, target, in.Request)
	})
	command(b, "secrets.delete", func(ctx context.Context, cp *app.CtrlPlane, in secretInput) (any, error) {
		target, err := ownedInstance(ctx, cp, in.InstanceID)
		if err != nil {
			return nil, err
		}

		if in.Key == "" {
			return nil, badRequest("Secret key is required.")
		}

		return ack(cp.Secrets.Delete(ctx, target, in.Key))
	})
	command(b, "providers.test", func(ctx context.Context, cp *app.CtrlPlane, in namedInput) (any, error) {
		return cp.Admin.TestProviderHealth(ctx, in.Name)
	})
	command(b, "providers.purge", func(ctx context.Context, cp *app.CtrlPlane, in namedInput) (any, error) {
		return purgeProvider(ctx, cp, in.Name)
	})
	command(b, "tenants.create", func(ctx context.Context, cp *app.CtrlPlane, in admin.CreateTenantRequest) (any, error) {
		if in.Name == "" {
			return nil, badRequest("Name is required.")
		}

		return cp.Admin.CreateTenant(ctx, in)
	})
	command(b, "tenants.unsuspend", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		if _, err := parseID(in.ID, id.PrefixTenant); err != nil {
			return nil, err
		}

		return ack(cp.Admin.UnsuspendTenant(ctx, in.ID))
	})
	command(b, "tenants.delete", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		if _, err := parseID(in.ID, id.PrefixTenant); err != nil {
			return nil, err
		}

		return ack(cp.Admin.DeleteTenant(ctx, in.ID))
	})
	command(b, "tenants.suspend", func(ctx context.Context, cp *app.CtrlPlane, in reasonInput) (any, error) {
		if _, err := parseID(in.ID, id.PrefixTenant); err != nil {
			return nil, err
		}

		if in.Reason == "" {
			return nil, badRequest("A suspension reason is required.")
		}

		return ack(cp.Admin.SuspendTenant(ctx, in.ID, in.Reason))
	})
	command(b, "datacenters.status", func(ctx context.Context, cp *app.CtrlPlane, in statusInput) (any, error) {
		target, err := parseID(in.ID, id.PrefixDatacenter)
		if err != nil {
			return nil, err
		}

		return ack(cp.Datacenters.SetStatus(ctx, target, in.Status))
	})
	command(b, "bootstrap.retry", func(ctx context.Context, cp *app.CtrlPlane, in entityInput) (any, error) {
		return retryBootstrap(ctx, cp, in.ID)
	})
}
