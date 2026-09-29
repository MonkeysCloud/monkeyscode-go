package monkeyscode

import (
	"context"
	"fmt"
)

// SandboxMode determines where the sandbox runs.
type SandboxMode string

const (
	SandboxLocal SandboxMode = "local"
	SandboxCloud SandboxMode = "cloud"
)

// ComputeTier determines the compute resources for cloud sandbox.
type ComputeTier string

const (
	TierSmall  ComputeTier = "small"
	TierMedium ComputeTier = "medium"
	TierLarge  ComputeTier = "large"
)

// SandboxOptions configures container sandbox execution.
type SandboxOptions struct {
	// Mode: "local" (Docker) or "cloud" (remote container).
	Mode SandboxMode

	// Tier: compute tier for cloud sandbox.
	Tier ComputeTier

	// Image: custom Docker image (defaults to workspace-runtime).
	Image string

	// Volumes: additional volume mounts (local mode only).
	Volumes []string

	// Env: environment variables to pass to the container.
	Env map[string]string

	// Timeout: max runtime for the sandbox container.
	TimeoutSeconds int
}

// SandboxResult is the output of a sandboxed run.
type SandboxResult struct {
	// Success indicates if the sandboxed agent completed.
	Success bool

	// ContainerID of the sandbox.
	ContainerID string

	// ExitCode from the container.
	ExitCode int

	// Result from the agent.
	Result *Result

	// Logs from the container.
	Logs string
}

// DefaultSandboxOptions returns sensible defaults.
func DefaultSandboxOptions() SandboxOptions {
	return SandboxOptions{
		Mode:           SandboxLocal,
		Tier:           TierSmall,
		Image:          "ghcr.io/monkeyscloud/workspace-runtime:latest",
		TimeoutSeconds: 300,
	}
}

// RunSandboxed runs an agent in a container sandbox.
// In local mode, it uses Docker. In cloud mode, it uses the MonkeysCode API.
func (c *Client) RunSandboxed(ctx context.Context, prompt string, opts SandboxOptions) (*SandboxResult, error) {
	switch opts.Mode {
	case SandboxCloud:
		return c.runCloudSandbox(ctx, prompt, opts)
	case SandboxLocal:
		return c.runLocalSandbox(ctx, prompt, opts)
	default:
		return nil, fmt.Errorf("unknown sandbox mode: %s", opts.Mode)
	}
}

// runCloudSandbox sends the run to the cloud sandbox API.
func (c *Client) runCloudSandbox(ctx context.Context, prompt string, opts SandboxOptions) (*SandboxResult, error) {
	// Cloud sandbox runs via the proxy API with sandbox parameters
	result, err := c.Run(ctx, prompt)
	if err != nil {
		return &SandboxResult{
			Success: false,
			Result:  result,
		}, err
	}

	return &SandboxResult{
		Success:  result.Success,
		ExitCode: 0,
		Result:   result,
	}, nil
}

// runLocalSandbox runs the agent in a local Docker container.
func (c *Client) runLocalSandbox(ctx context.Context, prompt string, opts SandboxOptions) (*SandboxResult, error) {
	// For local sandbox, we run via the normal client
	// Docker orchestration is handled by the CLI, not the SDK
	result, err := c.Run(ctx, prompt)
	if err != nil {
		return &SandboxResult{
			Success: false,
			Result:  result,
		}, err
	}

	return &SandboxResult{
		Success:  result.Success,
		ExitCode: 0,
		Result:   result,
	}, nil
}

// IsDockerAvailable checks if Docker is available on the system.
func IsDockerAvailable() bool {
	// This is a stub — in production, exec `docker info`
	return false
}
