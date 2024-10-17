package environment

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/sdk/azdcore"
	"github.com/azure/azure-dev/cli/sdk/azdcore/common/ioc"
	"github.com/azure/azure-dev/cli/sdk/azdcore/contracts"
)

// Description is a metadata description of an environment returned for the `azd env list` command
type Description struct {
	// The name of the environment
	Name string
	// The path to the local .env file for the environment. Useful for IDEs like VS / VSCode
	DotEnvPath string
	// Specifies when the environment exists locally
	HasLocal bool
	// Specifies when the environment exists remotely
	HasRemote bool
	// Specifies when the environment is the default environment
	IsDefault bool
}

// Spec is the specification for creating a new environment
type Spec struct {
	Name         string
	Subscription string
	Location     string
	// suggest is the name that is offered as a suggestion if we need to prompt the user for an environment name.
	Examples []string
}

const DotEnvFileName = ".env"
const ConfigFileName = "config.json"

var (
	// Error returned when an environment with the specified name already exists
	ErrExists = errors.New("environment already exists")

	// Error returned when an environment with a specified name cannot be found
	ErrNotFound = errors.New("environment not found")

	// Error returned when an environment name is not specified
	ErrNameNotSpecified = errors.New("environment not specified")
)

// Manager is the interface used for managing instances of environments
type Manager interface {
	Create(ctx context.Context, spec Spec) (*Environment, error)

	List(ctx context.Context) ([]*Description, error)

	// Get returns the existing environment with the given name.
	// If the environment specified by the given name does not exist, ErrNotFound is returned.
	Get(ctx context.Context, name string) (*Environment, error)

	Save(ctx context.Context, env *Environment) error
	SaveWithOptions(ctx context.Context, env *Environment, options *SaveOptions) error
	Reload(ctx context.Context, env *Environment) error

	// Delete deletes the environment from local storage.
	Delete(ctx context.Context, name string) error

	EnvPath(env *Environment) string
	ConfigPath(env *Environment) string
}

type manager struct {
	local      DataStore
	remote     DataStore
	azdContext *azdcore.Context
}

// NewManager creates a new Manager instance
func NewManager(
	serviceLocator ioc.ServiceLocator,
	azdContext *azdcore.Context,
	local LocalDataStore,
	remoteConfig *contracts.RemoteConfig,
) (Manager, error) {
	var remote RemoteDataStore

	// Ideally we would have liked to inject the remote data store directly into the manager,
	// via the container but we can't do that because the remote data store is optional and the IoC
	// container doesn't support optional interface based dependencies.
	if remoteConfig != nil {
		err := serviceLocator.ResolveNamed(remoteConfig.Backend, &remote)
		if err != nil {
			if errors.Is(err, ioc.ErrResolveInstance) {
				return nil, fmt.Errorf(
					"remote state configuration is invalid. The specified backend '%s' is not valid. Valid values are '%s'.",
					remoteConfig.Backend,
					strings.Join(ValidRemoteKinds, ","),
				)
			}

			return nil, fmt.Errorf("resolving remote state data store: %w", err)
		}
	}

	return &manager{
		azdContext: azdContext,
		local:      local,
		remote:     remote,
	}, nil
}

func (m *manager) Create(ctx context.Context, spec Spec) (*Environment, error) {
	if spec.Name != "" && !IsValidEnvironmentName(spec.Name) {
		errMsg := invalidEnvironmentNameMsg(spec.Name)
		return nil, errors.New(errMsg)
	}

	if err := m.ensureValidEnvironmentName(ctx, &spec); err != nil {
		return nil, err
	}

	// Ensure the environment does not already exist:
	_, err := m.Get(ctx, spec.Name)
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return nil, fmt.Errorf("checking for existing environment: %w", err)
	default:
		return nil, fmt.Errorf("environment '%s' already exists", spec.Name)
	}

	env := New(spec.Name)

	if spec.Subscription != "" {
		env.SetSubscriptionId(spec.Subscription)
	}

	if spec.Location != "" {
		env.SetLocation(spec.Location)
	}

	if err := m.SaveWithOptions(ctx, env, &SaveOptions{IsNew: true}); err != nil {
		return nil, err
	}

	return env, nil
}

// ConfigPath returns the path to the environment config file
func (m *manager) ConfigPath(env *Environment) string {
	return m.local.ConfigPath(env)
}

// EnvPath returns the path to the environment .env file
func (m *manager) EnvPath(env *Environment) string {
	return m.local.EnvPath(env)
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
			Name:       env.Name,
			HasLocal:   true,
			DotEnvPath: env.DotEnvPath,
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

	slices.SortFunc(allEnvs, func(a, b *Description) int {
		return strings.Compare(a.Name, b.Name)
	})

	return allEnvs, nil
}

// Get returns the environment instance for the specified environment name
func (m *manager) Get(ctx context.Context, name string) (*Environment, error) {
	if name == "" {
		return nil, ErrNameNotSpecified
	}

	localEnv, err := m.local.Get(ctx, name)
	if err != nil {
		if m.remote == nil {
			return nil, err
		}

		remoteEnv, err := m.remote.Get(ctx, name)
		if err != nil {
			return nil, err
		}

		if err := m.local.Save(ctx, remoteEnv, nil); err != nil {
			return nil, err
		}

		localEnv = remoteEnv
	}

	// Ensures local environment variable name is synced with the environment name
	envName, ok := localEnv.LookupEnv(EnvNameEnvVarName)
	if !ok || envName != name {
		localEnv.DotenvSet(EnvNameEnvVarName, name)
		if err := m.Save(ctx, localEnv); err != nil {
			return nil, err
		}
	}

	return localEnv, nil
}

// Save saves the environment to the persistent data store
func (m *manager) Save(ctx context.Context, env *Environment) error {
	return m.SaveWithOptions(ctx, env, nil)
}

// Save saves the environment to the persistent data store with the specified options
func (m *manager) SaveWithOptions(ctx context.Context, env *Environment, options *SaveOptions) error {
	if options == nil {
		options = &SaveOptions{}
	}

	if err := m.local.Save(ctx, env, options); err != nil {
		return fmt.Errorf("saving local environment, %w", err)
	}

	if m.remote == nil {
		return nil
	}

	if err := m.remote.Save(ctx, env, options); err != nil {
		return fmt.Errorf("saving remote environment, %w", err)
	}

	return nil
}

// Reload reloads the environment from the persistent data store
func (m *manager) Reload(ctx context.Context, env *Environment) error {
	return m.local.Reload(ctx, env)
}

func (m *manager) Delete(ctx context.Context, name string) error {
	if name == "" {
		return ErrNameNotSpecified
	}

	err := m.local.Delete(ctx, name)
	if err != nil {
		return err
	}

	defaultEnvName, err := m.azdContext.GetDefaultEnvironmentName()
	if err != nil {
		return fmt.Errorf("getting default environment: %w", err)
	}

	if defaultEnvName == name {
		err = m.azdContext.SetProjectState(azdcore.ProjectState{DefaultEnvironment: ""})
		if err != nil {
			return fmt.Errorf("clearing default environment: %w", err)
		}
	}

	return nil
}

// ensureValidEnvironmentName ensures the environment name is valid, if it is not, an error is printed
// and the user is prompted for a new name.
func (m *manager) ensureValidEnvironmentName(ctx context.Context, spec *Spec) error {
	exampleText := ""
	if len(spec.Examples) > 0 {
		exampleText = "\n\nExamples:"
	}

	for _, example := range spec.Examples {
		exampleText += fmt.Sprintf("\n  %s", example)
	}

	for !IsValidEnvironmentName(spec.Name) {
		return errors.New(invalidEnvironmentNameMsg(spec.Name))
	}

	return nil
}

func invalidEnvironmentNameMsg(environmentName string) string {
	return fmt.Sprintf(
		"environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
		environmentName,
	)
}
