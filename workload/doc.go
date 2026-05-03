// Package workload models a deployable workload as a top-level
// resource that owns N replica Instances. It maps to Kubernetes'
// Deployment in role: a Workload carries the spec (image, env,
// resources, ReplicaCount) and ctrlplane orchestrates per-replica
// container/pod creation through the provider layer.
//
// Entity relationships:
//
//	Workload 1:N Instance     (replicas; cascade delete)
//	Workload 1:N Release      (version history of the spec)
//	Workload 1:N Deployment   (rollout event log)
//	Release  1:N Deployment   (a release can be re-rolled out)
//
// Replica orchestration is a ctrlplane concern. Providers stay
// per-Pod (one Provision call = one container/pod). When a Workload
// scales from N=1 to N=3, the Workload service calls
// instance.Service.Create three times rather than asking the
// provider to "make 3 of these"; the docker provider creates 3
// independent containers, and the kubernetes provider creates 3
// independent Pods. This keeps the provider interface trivial and
// makes rollout strategies (rolling/blue-green/canary) uniform
// across providers.
package workload
