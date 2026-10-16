package obs

import "go.opentelemetry.io/otel/attribute"

// Span attribute keys. Values are counts, booleans, enums and opaque
// resource IDs. Tenant, subject and user identifiers, names, URLs, error
// messages and payloads never go on a span.
const (
	AttrProviderType      = attribute.Key("ctrlplane.provider.type")
	AttrProviderOperation = attribute.Key("ctrlplane.provider.operation")
	AttrProviderHealthy   = attribute.Key("ctrlplane.provider.healthy")
	AttrProvidersChecked  = attribute.Key("ctrlplane.providers.checked")
	AttrProvidersFailing  = attribute.Key("ctrlplane.providers.unhealthy")
	AttrSourceType        = attribute.Key("ctrlplane.source.type")
	AttrInstanceID        = attribute.Key("ctrlplane.instance.id")
	AttrDatacenterID      = attribute.Key("ctrlplane.datacenter.id")

	AttrDeploymentID       = attribute.Key("ctrlplane.deployment.id")
	AttrDeploymentStrategy = attribute.Key("ctrlplane.deployment.strategy")
	AttrDeploymentRollback = attribute.Key("ctrlplane.deployment.rollback")
	AttrDeploymentServices = attribute.Key("ctrlplane.deployment.services")

	AttrReadinessPolls   = attribute.Key("ctrlplane.readiness.polls")
	AttrReadinessOutcome = attribute.Key("ctrlplane.readiness.outcome")

	AttrReconcileDatacenters = attribute.Key("ctrlplane.reconcile.datacenters")
	AttrReconcileFailed      = attribute.Key("ctrlplane.reconcile.failed")

	AttrBootstrapID          = attribute.Key("ctrlplane.bootstrap.id")
	AttrBootstrapHook        = attribute.Key("ctrlplane.bootstrap.hook")
	AttrBootstrapDesired     = attribute.Key("ctrlplane.bootstrap.desired")
	AttrBootstrapProvisioned = attribute.Key("ctrlplane.bootstrap.provisioned")
	AttrBootstrapRetired     = attribute.Key("ctrlplane.bootstrap.retired")
	AttrBootstrapFailed      = attribute.Key("ctrlplane.bootstrap.failed")

	AttrGCTenants       = attribute.Key("ctrlplane.gc.tenants")
	AttrGCTenantsFailed = attribute.Key("ctrlplane.gc.tenants_failed")
	AttrGCReaped        = attribute.Key("ctrlplane.gc.reaped")

	AttrHealthCheckID   = attribute.Key("ctrlplane.health.check.id")
	AttrHealthCheckType = attribute.Key("ctrlplane.health.check_type")
	AttrHealthStatus    = attribute.Key("ctrlplane.health.status")

	AttrWorkloadID      = attribute.Key("ctrlplane.workload.id")
	AttrWorkloadReplica = attribute.Key("ctrlplane.workload.replica")
	AttrReplicaAdopted  = attribute.Key("ctrlplane.workload.replica_adopted")

	AttrManagedStep    = attribute.Key("ctrlplane.managed.step")
	AttrManagedRefusal = attribute.Key("ctrlplane.managed.refusal")
)
