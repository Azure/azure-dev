// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// betaUserConfigServiceOverride implements the beta-only UserConfigService.IsFeatureEnabled method.
// It collects every opt-in source on the host so extensions do not assemble them.
type betaUserConfigServiceOverride struct {
	userConfig  *userConfigService
	environment *environmentService
	lookupEnv   func(string) (string, bool)
}

var _ BetaUserConfigServiceIsFeatureEnabledOverride = (*betaUserConfigServiceOverride)(nil)

// IsFeatureEnabled reports whether the feature is enabled in any source.
// Sources are user config alpha.<id> or alpha.all set to on, the host process environment,
// the azd environment dotenv values, and the env map of every azure.yaml service.
// A true value in one source is never overridden by another source.
// Missing project or environment sources are skipped.
func (o *betaUserConfigServiceOverride) IsFeatureEnabled(
	ctx context.Context,
	req *v1beta.IsFeatureEnabledRequest,
) (*v1beta.IsFeatureEnabledResponse, error) {
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	feature := alpha.Feature{Id: id, EnvVar: req.GetEnvVar()}
	if feature.EnvVar == "" {
		if registered, found := alpha.GetFeature(alpha.FeatureId(id)); found {
			feature.EnvVar = registered.EnvVar
		}
	}

	lookupEnv := o.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	processEnv := map[string]string{}
	for _, name := range feature.EnvVarNames() {
		if value, ok := lookupEnv(name); ok {
			processEnv[name] = value
		}
	}

	envSources := []map[string]string{processEnv}
	projectSources, err := o.projectSources(ctx, req.GetEnvName())
	if err != nil {
		return nil, err
	}
	envSources = append(envSources, projectSources...)

	return &v1beta.IsFeatureEnabledResponse{
		Enabled: feature.IsEnabledWithSources(o.userConfig.config, envSources...),
	}, nil
}

// projectSources returns the dotenv values and each service env map, without merging them.
func (o *betaUserConfigServiceOverride) projectSources(
	ctx context.Context,
	envName string,
) ([]map[string]string, error) {
	azdContext, err := o.environment.lazyAzdContext.GetValue()
	if errors.Is(err, azdcontext.ErrNoProject) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sources []map[string]string
	env, err := o.environment.resolveEnvironment(ctx, envName)
	switch {
	case err == nil:
		sources = append(sources, env.Dotenv())
	case envName == "" && (errors.Is(err, environment.ErrDefaultEnvironmentNotFound) ||
		errors.Is(err, environment.ErrNotFound)):
		// No environment selected.
	default:
		return nil, err
	}

	projectConfig, err := project.Load(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, fmt.Errorf("loading project: %w", err)
	}
	for _, service := range projectConfig.Services {
		if service != nil && len(service.Environment) > 0 {
			sources = append(sources, service.Environment.Raw())
		}
	}

	return sources, nil
}
