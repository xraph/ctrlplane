// Package template provides reusable workload blueprints. A Template
// captures the spec needed to instantiate a Workload — image, resources,
// env, secrets references, config files, volumes, ports, health check,
// labels and annotations — without any runtime state. Templates are
// authored directly via the template Service or forked from an existing
// Workload via CreateFromWorkload.
//
// Templates are tenant-scoped. The persistent schema lives in
// store/<backend>/template.go and is owned by this package — deploy
// no longer participates in template storage.
package template
