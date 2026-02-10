// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
)

type environmentService struct {
	azdext.UnimplementedEnvironmentServiceServer
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager *lazy.Lazy[environment.Manager]
}

func NewEnvironmentService(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
) azdext.EnvironmentServiceServer {
	return &environmentService{
		lazyAzdContext: lazyAzdContext,
		lazyEnvManager: lazyEnvManager,
	}
}

func (s *environmentService) List(ctx context.Context, req *azdext.EmptyRequest) (*azdext.EnvironmentListResponse, error) {
	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	envList, err := envManager.List(ctx)
	if err != nil {
		return nil, err
	}

	environments := make([]*azdext.EnvironmentDescription, len(envList))
	for i, env := range envList {
		environments[i] = &azdext.EnvironmentDescription{
			Name:    env.Name,
			Local:   env.HasLocal,
			Remote:  env.HasRemote,
			Default: env.IsDefault,
		}
	}

	return &azdext.EnvironmentListResponse{
		Environments: environments,
	}, nil
}

func (s *environmentService) GetCurrent(
	ctx context.Context,
	req *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	env, err := s.currentEnvironment(ctx)
	if err != nil {
		return nil, err
	}

	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{
			Name: env.Name(),
		},
	}, nil
}

func (s *environmentService) Get(
	ctx context.Context,
	req *azdext.GetEnvironmentRequest,
) (*azdext.EnvironmentResponse, error) {
	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	env, err := envManager.Get(ctx, req.Name)
	if err != nil {
		return nil, err
	}

	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{
			Name: env.Name(),
		},
	}, nil
}

func (s *environmentService) Select(
	ctx context.Context,
	req *azdext.SelectEnvironmentRequest,
) (*azdext.EmptyResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	env, err := envManager.Get(ctx, req.Name)
	if err != nil {
		return nil, err
	}

	projectState := azdcontext.ProjectState{
		DefaultEnvironment: env.Name(),
	}

	if err := azdContext.SetProjectState(projectState); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}

// GetValues retrieves all key-value pairs in the specified environment.
func (s *environmentService) GetValues(
	ctx context.Context,
	req *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	env, err := s.resolveEnvironment(ctx, req.Name)
	if err != nil {
		return nil, err
	}

	value := env.Dotenv()
	keyValues := make([]*azdext.KeyValue, len(value))

	i := 0
	for key, value := range value {
		keyValues[i] = &azdext.KeyValue{
			Key:   key,
			Value: value,
		}
		i++
	}

	return &azdext.KeyValueListResponse{
		KeyValues: keyValues,
	}, nil
}

// GetValue retrieves the value of a specific key in the specified environment.
func (s *environmentService) GetValue(ctx context.Context, req *azdext.GetEnvRequest) (*azdext.KeyValueResponse, error) {
	env, err := s.resolveEnvironment(ctx, req.EnvName)
	if err != nil {
		return nil, err
	}

	value := env.Getenv(req.Key)

	return &azdext.KeyValueResponse{
		Key:   req.Key,
		Value: value,
	}, nil
}

// SetValue sets the value of a key in the specified environment.
func (s *environmentService) SetValue(ctx context.Context, req *azdext.SetEnvRequest) (*azdext.EmptyResponse, error) {
	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	env, err := s.resolveEnvironment(ctx, req.EnvName)
	if err != nil {
		return nil, err
	}

	env.DotenvSet(req.Key, req.Value)
	if err := envManager.Save(ctx, env); err != nil {
		return nil, fmt.Errorf("failed to save environment: %w", err)
	}

	return &azdext.EmptyResponse{}, nil
}

func (s *environmentService) currentEnvironment(ctx context.Context) (*environment.Environment, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	defaultEnvironment, err := azdContext.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}

	if defaultEnvironment == "" {
		return nil, environment.ErrDefaultEnvironmentNotFound
	}

	env, err := envManager.Get(ctx, defaultEnvironment)
	if err != nil {
		return nil, err
	}

	return env, nil
}

// resolveEnvironment resolves the environment by name if provided, otherwise falls back to the default environment.
func (s *environmentService) resolveEnvironment(ctx context.Context, envName string) (*environment.Environment, error) {
	if envName == "" {
		return s.currentEnvironment(ctx)
	}

	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	return envManager.Get(ctx, envName)
}

// GetConfig retrieves a config value by path.
func (s *environmentService) GetConfig(
	ctx context.Context,
	req *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	env, err := s.resolveEnvironment(ctx, req.EnvName)
	if err != nil {
		return nil, err
	}

	value, exists := env.Config.Get(req.Path)

	var valueBytes []byte
	if exists {
		bytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}

		valueBytes = bytes
	}

	return &azdext.GetConfigResponse{
		Value: valueBytes,
		Found: exists,
	}, nil
}

// GetConfigString retrieves a config value as a string by path.
func (s *environmentService) GetConfigString(
	ctx context.Context,
	req *azdext.GetConfigStringRequest,
) (*azdext.GetConfigStringResponse, error) {
	env, err := s.resolveEnvironment(ctx, req.EnvName)
	if err != nil {
		return nil, err
	}

	value, exists := env.Config.GetString(req.Path)

	return &azdext.GetConfigStringResponse{
		Value: value,
		Found: exists,
	}, nil
}

// GetConfigSection retrieves a config section by path.
func (s *environmentService) GetConfigSection(
	ctx context.Context,
	req *azdext.GetConfigSectionRequest,
) (*azdext.GetConfigSectionResponse, error) {
	env, err := s.resolveEnvironment(ctx, req.EnvName)
	if err != nil {
		return nil, err
	}

	var section map[string]any

	exists, err := env.Config.GetSection(req.Path, &section)
	if err != nil {
		return nil, fmt.Errorf("failed to get section: %w", err)
	}

	var valueBytes []byte
	if exists {
		bytes, err := json.Marshal(section)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}

		valueBytes = bytes
	}

	return &azdext.GetConfigSectionResponse{
		Section: valueBytes,
		Found:   exists,
	}, nil
}

// SetConfig sets a config value at a given path.
func (s *environmentService) SetConfig(ctx context.Context, req *azdext.SetConfigRequest) (*azdext.EmptyResponse, error) {
	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	env, err := s.resolveEnvironment(ctx, req.EnvName)
	if err != nil {
		return nil, err
	}

	var value any
	if err := json.Unmarshal(req.Value, &value); err != nil {
		return nil, fmt.Errorf("failed to unmarshal value: %w", err)
	}

	if err := env.Config.Set(req.Path, value); err != nil {
		return nil, fmt.Errorf("failed to set value: %w", err)
	}

	if err := envManager.Save(ctx, env); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &azdext.EmptyResponse{}, nil
}

// UnsetConfig unsets a config value at a given path.
func (s *environmentService) UnsetConfig(
	ctx context.Context,
	req *azdext.UnsetConfigRequest,
) (*azdext.EmptyResponse, error) {
	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	env, err := s.resolveEnvironment(ctx, req.EnvName)
	if err != nil {
		return nil, err
	}

	if err := env.Config.Unset(req.Path); err != nil {
		return nil, fmt.Errorf("failed to unset value: %w", err)
	}

	if err := envManager.Save(ctx, env); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &azdext.EmptyResponse{}, nil
}
