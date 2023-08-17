package environment

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
)

type DataStore interface {
	Path(env *Environment) string
	ConfigPath(env *Environment) string
	List(ctx context.Context) ([]*contracts.EnvListEnvironment, error)
	Get(ctx context.Context, name string) (*Environment, error)
	Reload(ctx context.Context, env *Environment) error
	Save(ctx context.Context, env *Environment) error
}
