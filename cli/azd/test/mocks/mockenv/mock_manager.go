package mockenv

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/mock"
)

type MockEnvManager struct {
	mock.Mock
}

func (m *MockEnvManager) Create(ctx context.Context, spec environment.Spec) (*environment.Environment, error) {
	args := m.Called(ctx, spec)
	return args.Get(0).(*environment.Environment), args.Error(1)
}

func (m *MockEnvManager) LoadOrInitInteractive(ctx context.Context, name string) (*environment.Environment, error) {
	args := m.Called(ctx, name)
	return args.Get(0).(*environment.Environment), args.Error(1)
}

func (m *MockEnvManager) List(ctx context.Context) ([]*environment.Description, error) {
	args := m.Called(ctx)
	return args.Get(0).([]*environment.Description), args.Error(1)
}

func (m *MockEnvManager) Get(ctx context.Context, name string) (*environment.Environment, error) {
	args := m.Called(ctx, name)
	return args.Get(0).(*environment.Environment), args.Error(1)
}

func (m *MockEnvManager) Save(ctx context.Context, env *environment.Environment) error {
	args := m.Called(ctx, env)
	return args.Error(0)
}

func (m *MockEnvManager) Reload(ctx context.Context, env *environment.Environment) error {
	args := m.Called(ctx, env)
	return args.Error(0)
}

func (m *MockEnvManager) EnvPath(env *environment.Environment) string {
	args := m.Called(env)
	return args.String(0)
}

func (m *MockEnvManager) ConfigPath(env *environment.Environment) string {
	args := m.Called(env)
	return args.String(0)
}

func (m *MockEnvManager) Delete(ctx context.Context, name string) error {
	args := m.Called(name)
	return args.Error(0)
}
