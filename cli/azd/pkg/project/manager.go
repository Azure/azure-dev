package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

type Manager interface {
	ReadProject(
		ctx context.Context,
		projectPath string,
		env *environment.Environment,
	)
	NewProject(ctx context.Context, path string, name string) (*Project, error)
}

type manager struct {
}
