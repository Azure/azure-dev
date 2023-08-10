package environment

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
)

type LocalDataStore interface {
	Path(env *Environment) string
	ConfigPath(env *Environment) string
	Reload(ctx context.Context, env *Environment) error
	RemoteDataStore
}

type RemoteDataStore interface {
	List(ctx context.Context) ([]contracts.EnvListEnvironment, error)
	Get(ctx context.Context, name string) (*Environment, error)
	Create(ctx context.Context, name string) (*Environment, error)
	Delete(ctx context.Context, name string) error
	Values(ctx context.Context) (map[string]string, error)
	Refresh(ctx context.Context) error
	Save(ctx context.Context, env *Environment) error
}
