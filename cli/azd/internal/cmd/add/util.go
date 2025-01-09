// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func validateServiceName(name string, prj *project.ProjectConfig) error {
	err := names.ValidateLabelName(name)
	if err != nil {
		return err
	}

	if _, exists := prj.Services[name]; exists {
		return fmt.Errorf("service with name '%s' already exists", name)
	}

	return nil
}

func validateResourceName(name string, prj *project.ProjectConfig) error {
	err := names.ValidateLabelName(name)
	if err != nil {
		return err
	}

	if _, exists := prj.Resources[name]; exists {
		return fmt.Errorf("resource with name '%s' already exists", name)
	}

	return nil
}

// promptDir prompts the user to input a valid directory.
func promptDir(
	ctx context.Context,
	console input.Console,
	message string) (string, error) {
	for {
		path, err := console.PromptFs(ctx, input.ConsoleOptions{
			Message: message,
		}, input.FsOptions{
			SuggestOpts: input.FsSuggestOptions{
				ExcludeFiles: true,
			},
		})
		if err != nil {
			return "", err
		}

		fs, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) || fs != nil && !fs.IsDir() {
			console.Message(ctx, fmt.Sprintf("'%s' is not a valid directory", path))
			continue
		}

		if err != nil {
			return "", err
		}

		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}

		return path, err
	}
}

// promptDockerfile prompts the user to input a valid Dockerfile path or directory,
// returning the absolute Dockerfile path.
func promptDockerfile(
	ctx context.Context,
	console input.Console,
	message string) (string, error) {
	for {
		path, err := console.PromptFs(ctx, input.ConsoleOptions{
			Message: message,
		}, input.FsOptions{})
		if err != nil {
			return "", err
		}

		fs, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			console.Message(ctx, fmt.Sprintf("'%s' is not a valid file or directory", path))
			continue
		}

		if err != nil {
			return "", err
		}

		if fs.IsDir() {
			filePath := filepath.Join(path, "Dockerfile")
			file, err := os.Stat(filePath)
			if err != nil || file != nil && file.IsDir() {
				console.Message(
					ctx,
					fmt.Sprintf("could not find 'Dockerfile' in '%s'. Hint: provide a direct path to a Dockerfile", path))
				continue
			}
		}

		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}

		return path, err
	}
}

// pathHasInfraModule returns true if there is a file named "<module>" or "<module.ext>" in path.
func pathHasInfraModule(path, module string) (bool, error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("error while iterating directory: %w", err)
	}

	return slices.ContainsFunc(files, func(file fs.DirEntry) bool {
		fileName := file.Name()
		fileNameNoExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		return !file.IsDir() && fileNameNoExt == module
	}), nil
}

func getEnvDetails(
	ctx context.Context,
	env *environment.Environment,
	subMgr *account.SubscriptionsManager) (ux.EnvironmentDetails, error) {
	details := ux.EnvironmentDetails{}
	subscription, err := subMgr.GetSubscription(ctx, env.GetSubscriptionId())
	if err != nil {
		return details, fmt.Errorf("getting subscription: %w", err)
	}

	location, err := subMgr.GetLocation(ctx, env.GetSubscriptionId(), env.GetLocation())
	if err != nil {
		return details, fmt.Errorf("getting location: %w", err)
	}
	details.Location = location.DisplayName

	var subscriptionDisplay string
	if v, err := strconv.ParseBool(os.Getenv("AZD_DEMO_MODE")); err == nil && v {
		subscriptionDisplay = subscription.Name
	} else {
		subscriptionDisplay = fmt.Sprintf("%s (%s)", subscription.Name, subscription.Id)
	}

	details.Subscription = subscriptionDisplay
	return details, nil
}
