package environment

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
)

type Description struct {
	Name      string
	HasLocal  bool
	HasRemote bool
	IsDefault bool
}

const DotEnvFileName = ".env"
const ConfigFileName = "config.json"

var (
	ErrEnvironmentExists   = errors.New("environment already exists")
	ErrEnvironmentNotFound = errors.New("environment not found")
)

type Manager interface {
	List(ctx context.Context) ([]*Description, error)
	Get(ctx context.Context, name string) (*Environment, error)
	Save(ctx context.Context, env *Environment) error
	Reload(ctx context.Context, env *Environment) error
	Path(env *Environment) string
	ConfigPath(env *Environment) string
}

type manager struct {
	local      LocalDataStore
	remote     RemoteDataStore
	azdContext *azdcontext.AzdContext
}

func NewManager(azdContext *azdcontext.AzdContext, local LocalDataStore) Manager {
	return &manager{
		azdContext: azdContext,
		local:      local,
	}
}

func (m *manager) ConfigPath(env *Environment) string {
	return m.local.ConfigPath(env)
}

func (m *manager) Path(env *Environment) string {
	return m.local.Path(env)
}

func (m *manager) List(ctx context.Context) ([]*Description, error) {
	envMap := map[string]*Description{}
	defaultEnvName, err := m.azdContext.GetDefaultEnvironmentName()
	if err != nil {
		defaultEnvName = ""
	}

	localEnvs, err := m.local.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("retrieving local environments, %w", err)
	}

	for _, env := range localEnvs {
		envMap[env.Name] = &Description{
			Name:     env.Name,
			HasLocal: true,
		}
	}

	if m.remote != nil {
		remoteEnvs, err := m.remote.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("retrieving remote environments, %w", err)
		}

		for _, env := range remoteEnvs {
			existing, has := envMap[env.Name]
			if !has {
				existing = &Description{
					Name:      env.Name,
					HasRemote: true,
				}
			} else {
				existing.HasRemote = true
			}
			envMap[env.Name] = existing
		}

	}

	allEnvs := []*Description{}
	for _, env := range envMap {
		env.IsDefault = env.Name == defaultEnvName
		allEnvs = append(allEnvs, env)
	}

	return allEnvs, nil
}

func (m *manager) Get(ctx context.Context, name string) (*Environment, error) {
	localEnv, err := m.local.Get(ctx, name)
	if err != nil {
		if m.remote == nil {
			return nil, err
		}

		remoteEnv, err := m.remote.Get(ctx, name)
		if err != nil {
			return nil, err
		}

		localEnv, err = m.local.Create(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("creating local environment, %w", err)
		}

		localEnv.dotenv = remoteEnv.dotenv
		localEnv.Config = remoteEnv.Config
	}

	return localEnv, nil
}

func (m *manager) Save(ctx context.Context, env *Environment) error {
	return m.local.Save(ctx, env)
}

func (m *manager) Reload(ctx context.Context, env *Environment) error {
	return m.local.Reload(ctx, env)
}
