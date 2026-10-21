// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore featureflags

// Package featureflags resolves agents extension preview opt-ins.
package featureflags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Feature declares one extension opt-in.
type Feature struct {
	// ID is the user config key under alpha.
	ID string
	// Description explains the capability enabled by this feature.
	Description string
	// EnvVar is the exact environment key read for this feature.
	EnvVar string
}

const (
	alphaKey     = "alpha"
	enabledValue = "on"
)

// Host errors for an absent optional scope. They match the sentinels
// azdcontext.ErrNoProject and
// environment.ErrDefaultEnvironmentNotFound without importing
// those packages and their dependencies.
var (
	errNoProject                  = errors.New("no project exists; to create a new project, run `azd init`")
	errDefaultEnvironmentNotFound = errors.New("default environment not found")
)

type azdClient interface {
	Project() azdext.ProjectServiceClient
	Environment() azdext.EnvironmentServiceClient
	UserConfig() azdext.UserConfigServiceClient
}

// IsEnabled resolves feature for one invocation.
// Pass ExtensionContext.Environment to honor -e/--environment.
// An empty environmentName uses the project default, if one exists.
// Loading failures are returned even when another source enables it.
func IsEnabled(
	ctx context.Context,
	client *azdext.AzdClient,
	feature Feature,
	environmentName string,
) (bool, error) {
	if client == nil {
		return false, errors.New("featureflags.IsEnabled: client must not be nil")
	}
	if feature.ID == "" || feature.EnvVar == "" {
		return false, errors.New("featureflags.IsEnabled: feature ID and EnvVar must not be empty")
	}

	return isEnabled(ctx, client, feature, environmentName)
}

func isEnabled(
	ctx context.Context,
	client azdClient,
	feature Feature,
	environmentName string,
) (bool, error) {
	enabled := envEnabled(os.Getenv(feature.EnvVar))

	configResponse, err := client.UserConfig().Get(ctx, &azdext.GetUserConfigRequest{Path: alphaKey})
	if err != nil {
		return false, fmt.Errorf("reading user config for feature %q: %w", feature.ID, err)
	}
	if configResponse == nil {
		return false, fmt.Errorf("reading user config for feature %q: missing response", feature.ID)
	}
	if configResponse.Found {
		var alphaValues any
		if err := json.Unmarshal(configResponse.Value, &alphaValues); err != nil {
			return false, fmt.Errorf("decoding alpha user config for feature %q: %w", feature.ID, err)
		}
		enabled = configEnabled(alphaValues, feature.ID) || enabled
	}

	projectResponse, err := client.Project().Get(ctx, &azdext.EmptyRequest{})
	switch {
	case err != nil && environmentName == "" && isAbsentScope(err, errNoProject):
		// Without a project, neither local scope is available.
		return enabled, nil
	case err != nil:
		return false, fmt.Errorf("loading project for feature %q: %w", feature.ID, err)
	case projectResponse.GetProject() == nil:
		return false, fmt.Errorf("loading project for feature %q: missing project response", feature.ID)
	}

	valuesResponse, err := client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: environmentName,
	})
	switch {
	case err != nil && environmentName == "" && isAbsentScope(err, errDefaultEnvironmentNotFound):
		// A project need not have a default environment.
	case err != nil:
		return false, fmt.Errorf("reading azd environment %q for feature %q: %w", environmentName, feature.ID, err)
	case valuesResponse == nil:
		return false, fmt.Errorf("reading azd environment for feature %q: missing response", feature.ID)
	default:
		for _, value := range valuesResponse.KeyValues {
			if value.GetKey() == feature.EnvVar && envEnabled(value.GetValue()) {
				enabled = true
			}
		}
	}

	// Each service counts independently; false never hides another true.
	for _, service := range projectResponse.Project.Services {
		if envEnabled(service.GetEnvironment()[feature.EnvVar]) {
			enabled = true
		}
	}

	return enabled, nil
}

// envEnabled reports whether value is a true strconv.ParseBool value.
// False, empty, and unparsable values contribute no signal.
func envEnabled(value string) bool {
	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

// configEnabled matches "on" (any case) only at alpha.<id>.
// Other values, including non-string values, contribute no signal.
func configEnabled(alphaValues any, id string) bool {
	userConfig := config.NewConfig(map[string]any{alphaKey: alphaValues})
	value, ok := userConfig.GetString(alphaKey + "." + id)
	return ok && strings.EqualFold(value, enabledValue)
}

func isAbsentScope(err error, sentinel error) bool {
	// The host sends these sentinels as Unknown, without details.
	// Match the exact sentinel, never a broad transport status.
	// A plain error converts to Unknown with its own message.
	st, _ := status.FromError(err)
	return st.Code() == codes.Unknown &&
		st.Message() == sentinel.Error() && len(st.Proto().Details) == 0
}
