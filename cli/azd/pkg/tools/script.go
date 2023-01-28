package tools

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// Utility to easily execute a bash script across platforms
type Script interface {
	Execute(ctx context.Context, scriptPath string, interactive bool) (exec.RunResult, error)
}
