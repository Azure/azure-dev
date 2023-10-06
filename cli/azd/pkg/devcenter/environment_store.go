package devcenter

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"golang.org/x/exp/slices"
)

const (
	ConfigPath                                 = "platform.config"
	RemoteKindDevCenter environment.RemoteKind = "devcenter"
)

// EnvironmentStore is a remote environment data store for devcenter environments
type EnvironmentStore struct {
	config          *Config
	devCenterClient devcentersdk.DevCenterClient
	prompter        *Prompter
	manager         Manager
}

// NewEnvironmentStore creates a new devcenter environment store
func NewEnvironmentStore(
	config *Config,
	devCenterClient devcentersdk.DevCenterClient,
	prompter *Prompter,
	manager Manager,
) environment.RemoteDataStore {
	return &EnvironmentStore{
		config:          config,
		devCenterClient: devCenterClient,
		prompter:        prompter,
		manager:         manager,
	}
}

// EnvPath returns the path for the environment
func (s *EnvironmentStore) EnvPath(env *environment.Environment) string {
	return fmt.Sprintf("projects/%s/users/me/environments/%s", s.config.Project, env.GetEnvName())
}

// ConfigPath returns the path for the environment configuration
func (s *EnvironmentStore) ConfigPath(env *environment.Environment) string {
	return ""
}

// List returns a list of environments for the devcenter configuration
func (s *EnvironmentStore) List(ctx context.Context) ([]*contracts.EnvListEnvironment, error) {
	// If we don't have a valid devcenter configuration yet
	// then prompt the user to initialize the correct configuration then provide the listing
	if err := s.config.EnsureValid(); err != nil {
		updatedConfig, err := s.prompter.PromptForValues(ctx)
		if err != nil {
			return []*contracts.EnvListEnvironment{}, nil
		}

		s.config = updatedConfig
	}

	environmentListResponse, err := s.devCenterClient.
		DevCenterByName(s.config.Name).
		ProjectByName(s.config.Project).
		Environments().
		Get(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to get devcenter environment list: %w", err)
	}

	// Filter the environment list to those matching the configured environment definition
	matches := []*contracts.EnvListEnvironment{}
	for _, environment := range environmentListResponse.Value {
		if environment.EnvironmentDefinitionName == s.config.EnvironmentDefinition {
			matches = append(matches, &contracts.EnvListEnvironment{
				Name:       environment.Name,
				DotEnvPath: environment.ResourceGroupId,
			})
		}
	}

	return matches, nil
}

// Get returns the environment for the given name
func (s *EnvironmentStore) Get(ctx context.Context, name string) (*environment.Environment, error) {
	// If the devcenter configuration is not valid then we don't have enough information to query for the environment
	if err := s.config.EnsureValid(); err != nil {
		return nil, fmt.Errorf("%s %w", name, environment.ErrNotFound)
	}

	envs, err := s.List(ctx)
	if err != nil {
		return nil, err
	}

	matchingIndex := slices.IndexFunc(envs, func(env *contracts.EnvListEnvironment) bool {
		return env.Name == name
	})

	if matchingIndex < 0 {
		return nil, fmt.Errorf("%s %w", name, environment.ErrNotFound)
	}

	matchingEnv := envs[matchingIndex]
	env := environment.New(matchingEnv.Name)

	if err := s.Reload(ctx, env); err != nil {
		return nil, err
	}

	return env, nil
}

// Reload reloads the environment from the remote data store
func (s *EnvironmentStore) Reload(ctx context.Context, env *environment.Environment) error {
	environment, err := s.devCenterClient.
		DevCenterByName(s.config.Name).
		ProjectByName(s.config.Project).
		EnvironmentByName(env.GetEnvName()).
		Get(ctx)

	if err != nil {
		return fmt.Errorf("failed to get devcenter environment: %w", err)
	}

	outputs, err := s.manager.Outputs(ctx, environment)
	if err != nil {
		return fmt.Errorf("failed to get environment outputs: %w", err)
	}

	// Set the environment variables for the environment
	for key, outputParam := range outputs {
		env.DotenvSet(key, fmt.Sprintf("%v", outputParam.Value))
	}

	// Set the devcenter configuration for the environment
	if err := env.Config.Set(DevCenterNamePath, s.config.Name); err != nil {
		return err
	}
	if err := env.Config.Set(DevCenterProjectPath, s.config.Project); err != nil {
		return err
	}
	if err := env.Config.Set(DevCenterCatalogPath, s.config.Catalog); err != nil {
		return err
	}
	if err := env.Config.Set(DevCenterEnvTypePath, s.config.EnvironmentType); err != nil {
		return err
	}
	if err := env.Config.Set(DevCenterEnvDefinitionPath, s.config.EnvironmentDefinition); err != nil {
		return err
	}

	return nil
}

// Save saves the environment to the remote data store
// DevCenter doesn't implement any APIs for saving environment configuration / metadata
// outside of the environment definition itself or the ARM deployment outputs
func (s *EnvironmentStore) Save(ctx context.Context, env *environment.Environment) error {
	return nil
}
