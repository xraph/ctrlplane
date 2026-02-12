package health

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// CommandChecker executes a command and checks the exit code.
type CommandChecker struct{}

// NewCommandChecker creates a new command health checker.
func NewCommandChecker() *CommandChecker {
	return &CommandChecker{}
}

// Type returns the check type this checker handles.
func (c *CommandChecker) Type() CheckType {
	return CheckCommand
}

// Check executes the command specified in the health check target and returns the result.
func (c *CommandChecker) Check(ctx context.Context, check *HealthCheck) (*HealthResult, error) {
	start := time.Now()

	timeout := check.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", check.Target) //nolint:gosec // target is operator-configured

	output, err := cmd.CombinedOutput()

	result := &HealthResult{
		Entity:     ctrlplane.NewEntity(id.PrefixHealthResult),
		CheckID:    check.ID,
		InstanceID: check.InstanceID,
		TenantID:   check.TenantID,
		Latency:    time.Since(start),
		CheckedAt:  time.Now().UTC(),
	}

	if err != nil {
		result.Status = StatusUnhealthy
		result.Message = fmt.Sprintf("command failed: %v: %s", err, string(output))

		return result, nil
	}

	result.Status = StatusHealthy
	result.Message = string(output)

	return result, nil
}
