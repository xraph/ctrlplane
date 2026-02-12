package admin

// Quota defines resource limits per tenant.
type Quota struct {
	MaxInstances int `db:"max_instances"  json:"max_instances"`
	MaxCPUMillis int `db:"max_cpu_millis" json:"max_cpu_millis"`
	MaxMemoryMB  int `db:"max_memory_mb"  json:"max_memory_mb"`
	MaxDiskMB    int `db:"max_disk_mb"    json:"max_disk_mb"`
	MaxDomains   int `db:"max_domains"    json:"max_domains"`
	MaxSecrets   int `db:"max_secrets"    json:"max_secrets"`
}

// QuotaUsage shows current usage against quota limits.
type QuotaUsage struct {
	Tenant *Tenant       `json:"tenant"`
	Quota  Quota         `json:"quota"`
	Used   QuotaSnapshot `json:"used"`
}

// QuotaSnapshot captures a point-in-time usage count.
type QuotaSnapshot struct {
	Instances int `json:"instances"`
	CPUMillis int `json:"cpu_millis"`
	MemoryMB  int `json:"memory_mb"`
	DiskMB    int `json:"disk_mb"`
	Domains   int `json:"domains"`
	Secrets   int `json:"secrets"`
}
