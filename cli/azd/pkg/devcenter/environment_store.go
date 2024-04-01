package devcenter

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
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
	local           environment.LocalDataStore
}

// NewEnvironmentStore creates a new devcenter environment store
func NewEnvironmentStore(
	config *Config,
	devCenterClient devcentersdk.DevCenterClient,
	prompter *Prompter,
	manager Manager,
	local environment.LocalDataStore,
) environment.RemoteDataStore {
	return &EnvironmentStore{
		config:          config,
		devCenterClient: devCenterClient,
		prompter:        prompter,
		manager:         manager,
		local:           local,
	}
}

// EnvPath returns the path for the environment
func (s *EnvironmentStore) EnvPath(env *environment.Environment) string {
	return fmt.Sprintf("projects/%s/users/me/environments/%s", s.config.Project, env.Name())
}

// ConfigPath returns the path for the environment configuration
func (s *EnvironmentStore) ConfigPath(env *environment.Environment) string {
	return ""
}

// List returns a list of environments for the devcenter configuration
func (s *EnvironmentStore) List(ctx context.Context) ([]*contracts.EnvListEnvironment, error) {
	if err := s.ensureDevCenterConfig(ctx); err != nil {
		return nil, err
	}

	matches, err := s.matchingEnvironments(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get devcenter environment list: %w", err)
	}

	envListEnvs := []*contracts.EnvListEnvironment{}
	for _, environment := range matches {
		envListEnvs = append(envListEnvs, &contracts.EnvListEnvironment{
			Name:       environment.Name,
			DotEnvPath: environment.ResourceGroupId,
		})
	}

	return envListEnvs, nil
}

// Get returns the environment for the given name
func (s *EnvironmentStore) Get(ctx context.Context, name string) (*environment.Environment, error) {
	if err := s.ensureDevCenterConfig(ctx); err != nil {
		return nil, err
	}

	filter := func(env *devcentersdk.Environment) bool {
		return s.envDefFilter(env) && strings.EqualFold(env.Name, name)
	}

	matchingEnvs, err := s.matchingEnvironments(ctx, filter)
	if err != nil {
		return nil, err
	}

	if len(matchingEnvs) == 0 {
		return nil, fmt.Errorf("%s %w", name, environment.ErrNotFound)
	}

	if len(matchingEnvs) > 1 {
		return nil, fmt.Errorf("multiple environments found with name '%s'", name)
	}

	matchingEnv := matchingEnvs[0]
	env := environment.New(matchingEnv.Name)

	if err := s.Reload(ctx, env); err != nil {
		return nil, err
	}

	return env, nil
}

// Reload reloads the environment from the remote data store
func (s *EnvironmentStore) Reload(ctx context.Context, env *environment.Environment) error {
	filter := func(e *devcentersdk.Environment) bool {
		return s.envDefFilter(e) && strings.EqualFold(e.Name, env.Name())
	}

	envList, err := s.matchingEnvironments(ctx, filter)
	if err != nil {
		return err
	}

	if len(envList) != 1 {
		return environment.ErrNotFound
	}

	environment, err := s.devCenterClient.
		DevCenterByName(s.config.Name).
		ProjectByName(s.config.Project).
		EnvironmentsByUser(envList[0].User).
		EnvironmentByName(env.Name()).
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
	if err := env.Config.Set(DevCenterCatalogPath, environment.CatalogName); err != nil {
		return err
	}
	if err := env.Config.Set(DevCenterEnvTypePath, environment.EnvironmentType); err != nil {
		return err
	}
	if err := env.Config.Set(DevCenterEnvDefinitionPath, environment.EnvironmentDefinitionName); err != nil {
		return err
	}
	if err := env.Config.Set(DevCenterUserPath, environment.User); err != nil {
		return err
	}

	// Set the environment definition parameters
	for key, value := range environment.Parameters {
		path := fmt.Sprintf("%s.%s", ProvisionParametersConfigPath, key)
		if err := env.Config.Set(path, value); err != nil {
			return fmt.Errorf("failed setting config value %s: %w", path, err)
		}
	}

	return nil
}

// Save saves the environment to the remote data store
// DevCenter doesn't implement any APIs for saving environment configuration / metadata
// outside of the environment definition itself or the ARM deployment outputs
func (s *EnvironmentStore) Save(ctx context.Context, env *environment.Environment) error {
	if s.config.Project != "" {
		if err := env.Config.Set(DevCenterProjectPath, s.config.Project); err != nil {
			return err
		}
	}

	if s.config.EnvironmentType != "" {
		if err := env.Config.Set(DevCenterEnvTypePath, s.config.EnvironmentType); err != nil {
			return err
		}
	}

	return s.local.Save(ctx, env)
}

// Delete implements environment.RemoteDataStore.
// Since the remote data store doesn't store environment configuration / metadata,
// we only delete the local storage.
func (s *EnvironmentStore) Delete(ctx context.Context, name string) error {
	return s.local.Delete(ctx, name)
}

// matchingEnvironments returns a list of environments matching the configured environment definition
func (s *EnvironmentStore) matchingEnvironments(
	ctx context.Context,
	filter EnvironmentFilterPredicate,
) ([]*devcentersdk.Environment, error) {
	environmentListResponse, err := s.devCenterClient.
		DevCenterByName(s.config.Name).
		ProjectByName(s.config.Project).
		Environments().
		Get(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to get devcenter environment list: %w", err)
	}

	if filter == nil {
		filter = s.envDefFilter
	}

	// Filter the environment list to those matching the configured environment definition
	matches := []*devcentersdk.Environment{}
	for _, environment := range environmentListResponse.Value {
		if filter(environment) {
			matches = append(matches, environment)
		}
	}

	return matches, nil
}

func (s *EnvironmentStore) envDefFilter(env *devcentersdk.Environment) bool {
	return env.EnvironmentDefinitionName == s.config.EnvironmentDefinition
}

// Checks whether a valid dev center configuration exists
// If values are missing prompts the user to supply values used for the lifetime of this command
func (s *EnvironmentStore) ensureDevCenterConfig(ctx context.Context) error {
	// If we don't have a valid devcenter configuration yet
	// then prompt the user to initialize the correct configuration then provide the listing
	if err := s.config.EnsureValid(); err != nil {
		updatedConfig, err := s.prompter.PromptForConfig(ctx)
		if err != nil {
			return fmt.Errorf("DevCenter configuration is not valid. Confirm your configuration and try again, %w", err)
		}

		s.config = updatedConfig
	}

	return nil
}
