package components

// HealthCounts holds aggregate health status counts for display in widgets.
type HealthCounts struct {
	Healthy   int
	Degraded  int
	Unhealthy int
	Unknown   int
}
