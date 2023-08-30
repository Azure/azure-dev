package environment

import (
	"context"
	"errors"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"golang.org/x/exp/slices"
)

// Description is a metadata description of an environment returned for the `azd env list` command
type Description struct {
	Name      string
	HasLocal  bool
	HasRemote bool
	IsDefault bool
}

// Spec is the specification for creating a new environment
type Spec struct {
	Name         string
	Subscription string
	Location     string
	// suggest is the name that is offered as a suggestion if we need to prompt the user for an environment name.
	Suggest string
}

const DotEnvFileName = ".env"
const ConfigFileName = "config.json"

var (
	ErrExists   = errors.New("environment already exists")
	ErrNotFound = errors.New("environment not found")
)

// Manager is the interface used for managing instances of environments
type Manager interface {
	CreateInteractive(ctx context.Context, spec Spec) (*Environment, error)
	LoadOrCreateInteractive(ctx context.Context, name string) (*Environment, error)
	List(ctx context.Context) ([]*Description, error)
	Get(ctx context.Context, name string) (*Environment, error)
	Save(ctx context.Context, env *Environment) error
	Reload(ctx context.Context, env *Environment) error
	Path(env *Environment) string
	ConfigPath(env *Environment) string
}

type manager struct {
	local      DataStore
	remote     DataStore
	azdContext *azdcontext.AzdContext
	console    input.Console
}

// NewManager creates a new Manager instance
func NewManager(
	azdContext *azdcontext.AzdContext,
	console input.Console,
	local DataStore,
	remote DataStore,
) Manager {
	return &manager{
		azdContext: azdContext,
		local:      local,
		remote:     remote,
		console:    console,
	}
}

func (m *manager) LoadOrCreateInteractive(ctx context.Context, name string) (*Environment, error) {
	// If there's a default environment, use that
	if name == "" {
		if defaultName, err := m.azdContext.GetDefaultEnvironmentName(); err != nil {
			return nil, fmt.Errorf("getting default environment: %w", err)
		} else {
			name = defaultName
		}
	}

	env, err := m.Get(ctx, name)
	if err != nil && errors.Is(err, ErrNotFound) {
		env, err = m.CreateInteractive(ctx, Spec{
			Name: name,
		})
	}

	if err != nil {
		return nil, err
	}

	return env, nil
}

func (m *manager) CreateInteractive(ctx context.Context, spec Spec) (*Environment, error) {
	msg := fmt.Sprintf("Environment '%s' does not exist, would you like to create it?", spec.Name)
	shouldCreate, promptErr := m.console.Confirm(ctx, input.ConsoleOptions{
		Message:      msg,
		DefaultValue: true,
	})
	if promptErr != nil {
		return nil, fmt.Errorf("prompting to create environment '%s': %w", spec.Name, promptErr)
	}
	if !shouldCreate {
		return nil, fmt.Errorf("%w '%s'", ErrNotFound, spec.Name)
	}

	if err := m.ensureValidEnvironmentName(ctx, &spec); err != nil {
		errMsg := invalidEnvironmentNameMsg(spec.Name)
		m.console.Message(ctx, errMsg)
		return nil, fmt.Errorf(errMsg)
	}

	env := New(spec.Name, m.azdContext.EnvironmentRoot(spec.Name))
	err := m.Save(ctx, env)
	if err != nil {
		return nil, err
	}

	if spec.Subscription != "" {
		env.SetSubscriptionId(spec.Subscription)
	}

	if spec.Location != "" {
		env.SetLocation(spec.Location)
	}

	if err := m.Save(ctx, env); err != nil {
		return nil, err
	}

	return env, nil
}

// ConfigPath returns the path to the environment config file
func (m *manager) ConfigPath(env *Environment) string {
	return m.local.ConfigPath(env)
}

// Path returns the path to the environment .env file
func (m *manager) Path(env *Environment) string {
	return m.local.Path(env)
}

// List returns a list of all environments within the data store
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

	slices.SortFunc(allEnvs, func(a, b *Description) bool {
		return a.Name < b.Name
	})

	return allEnvs, nil
}

// Get returns the environment instance for the specified environment name
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

		if err := m.local.Save(ctx, remoteEnv); err != nil {
			return nil, err
		}

		localEnv = remoteEnv
	}

	return localEnv, nil
}

// Save saves the environment to the persistent data store
func (m *manager) Save(ctx context.Context, env *Environment) error {
	if err := m.local.Save(ctx, env); err != nil {
		return fmt.Errorf("saving local environment, %w", err)
	}

	if m.remote == nil {
		return nil
	}

	if err := m.remote.Save(ctx, env); err != nil {
		return fmt.Errorf("saving remote environment, %w", err)
	}

	return nil
}

// Reload reloads the environment from the persistent data store
func (m *manager) Reload(ctx context.Context, env *Environment) error {
	return m.local.Reload(ctx, env)
}

// ensureValidEnvironmentName ensures the environment name is valid, if it is not, an error is printed
// and the user is prompted for a new name.
func (m *manager) ensureValidEnvironmentName(ctx context.Context, spec *Spec) error {
	for !IsValidEnvironmentName(spec.Name) {
		userInput, err := m.console.Prompt(ctx, input.ConsoleOptions{
			Message: "Enter a new environment name:",
			Help: heredoc.Doc(`
			A unique string that can be used to differentiate copies of your application in Azure.

			This value is typically used by the infrastructure as code templates to name the resource group that contains
			the infrastructure for your application and to generate a unique suffix that is applied to resources to prevent
			naming collisions.`),
			DefaultValue: spec.Suggest,
		})

		if err != nil {
			return fmt.Errorf("reading environment name: %w", err)
		}

		spec.Name = userInput

		if !IsValidEnvironmentName(spec.Name) {
			m.console.Message(ctx, invalidEnvironmentNameMsg(spec.Name))
		}
	}

	return nil
}

func invalidEnvironmentNameMsg(environmentName string) string {
	return fmt.Sprintf(
		"environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
		environmentName,
	)
}
