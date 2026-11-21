// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulators"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

const providerName = "emulators"

func newProviderListener(
	resolveLocal func() (launcher, error), resolveProject func(string, string) (launcher, error),
) *cobra.Command {
	var addSucceeded atomic.Bool
	var listen *cobra.Command
	listen = azdext.NewListenCommand(func(host *azdext.ExtensionHost) {
		client := host.Client()
		definitions := emulators.All()
		items := emulatorAddItems(listen.Context(), definitions, resolveLocal)
		host.WithBetaAddProvider("Emulators", items, func(ctx context.Context, id string) error {
			addSucceeded.Store(false)
			for _, item := range items {
				if item.GetId() == id && item.GetDisabledReason() != "" {
					return &azdext.LocalError{
						Category: azdext.LocalErrorCategoryValidation, Code: azdext.AddItemUnavailableCode,
						Message: item.GetDisabledReason(),
					}
				}
			}
			err := addEmulator(ctx, client.Project(), client.Prompt(), definitions, id)
			addSucceeded.Store(err == nil && ctx.Err() == nil)
			return err
		})
		host.WithProvisioningProvider(providerName, func() azdext.ProvisioningProvider {
			return &emulatorProvider{
				definitions: definitions, resolve: resolveProject,
				currentEnvironment: func(ctx context.Context) (string, error) {
					current, err := client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
					if err != nil {
						return "", fmt.Errorf("resolving azd environment: %w", err)
					}
					return current.GetEnvironment().GetName(), nil
				},
			}
		})
	})
	runListen := listen.RunE
	listen.RunE = func(cmd *cobra.Command, args []string) error {
		addSucceeded.Store(false)
		err := runListen(cmd, args)
		return addListenerError(addSucceeded.Load(), err)
	}
	return listen
}

func addListenerError(addSucceeded bool, err error) error {
	// azd can reset the listener connection while tearing down a successful add.
	if addSucceeded && status.Code(err) == codes.Unavailable &&
		strings.HasPrefix(err.Error(), "receiver error: stream receive failed: ") &&
		strings.HasSuffix(err.Error(), "connection reset by peer") {
		return nil
	}
	return err
}

func emulatorAddItems(
	ctx context.Context, definitions []emulator.Definition, resolve func() (launcher, error),
) []*v1beta.AddItem {
	var dockerHost emulator.DockerHost
	service, dockerErr := resolve()
	if dockerErr == nil {
		dockerHost, dockerErr = service.CheckDocker(ctx)
	}

	items := make([]*v1beta.AddItem, 0, len(definitions))
	for _, definition := range definitions {
		item := &v1beta.AddItem{Id: definition.Name, DisplayName: definition.DisplayName}
		err := dockerErr
		if err == nil {
			err = dockerHost.CheckRequirements(definition)
		}
		if err != nil {
			item.DisabledReason = err.Error()
		}
		items = append(items, item)
	}
	return items
}

func addEmulator(
	ctx context.Context,
	project azdext.ProjectServiceClient,
	prompt azdext.PromptServiceClient,
	definitions []emulator.Definition,
	id string,
) error {
	index := slices.IndexFunc(definitions, func(definition emulator.Definition) bool { return definition.Name == id })
	if index < 0 {
		return fmt.Errorf("unknown emulator %q", id)
	}
	read := func(path string) (*azdext.GetProjectConfigValueResponse, error) {
		return project.GetConfigValue(ctx, &azdext.GetProjectConfigValueRequest{Path: path})
	}
	top, err := read("layers")
	if err != nil {
		return err
	}
	path := "infra.layers"
	var projectLayers []any
	topIndex := -1
	var infra map[string]any
	if top.GetFound() {
		path = "layers"
		var ok bool
		projectLayers, ok = top.GetValue().AsInterface().([]any)
		if !ok {
			return errors.New("layers must be a sequence")
		}
		infra = map[string]any{"layers": []any{}}
		emulatorIndex := -1
		for i, raw := range projectLayers {
			layer, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("layers[%d] must be a mapping", i)
			}
			if layer["name"] == providerName {
				topIndex = i
			}
			if entries, found := layer["infra"]; found {
				if err := scanEmulatorEntry(entries, i, &emulatorIndex); err != nil {
					return err
				}
			}
		}
		if emulatorIndex >= 0 {
			topIndex = emulatorIndex
		}
		if topIndex >= 0 {
			if entries, found := projectLayers[topIndex].(map[string]any)["infra"]; found {
				infra["layers"] = entries
			}
		}
	} else {
		response, err := read("infra")
		if err != nil {
			return err
		}
		if response.GetFound() && response.GetValue().GetStructValue() == nil {
			return errors.New("infra must be a mapping")
		}
		infra = response.GetValue().GetStructValue().AsMap()
	}
	layers, target, err := emulatorLayers(infra)
	if err != nil {
		return err
	}
	config := map[string]any{}
	if target >= 0 {
		entry := layers[target].(map[string]any)
		if raw, present := entry["config"]; present {
			existing, ok := raw.(map[string]any)
			if !ok {
				return errors.New("emulators layer config must be a mapping")
			}
			maps.Copy(config, existing)
		}
	}
	if _, exists := config[id]; exists {
		return &azdext.LocalError{
			Category: azdext.LocalErrorCategoryValidation, Code: azdext.AddItemUnavailableCode,
			Message: definitions[index].DisplayName + " is already declared in the emulators layer.",
		}
	}
	settings := map[string]any{}
	if definitions[index].HasDirectory {
		answer, err := prompt.Prompt(ctx, &azdext.PromptRequest{Options: &azdext.PromptOptions{
			Message: "Static-content directory to serve read-only (relative to the project)",
		}})
		if err != nil {
			return err
		}
		directory := strings.TrimSpace(answer.GetValue())
		if !validContentDirectory(directory) {
			return &azdext.LocalError{
				Category: azdext.LocalErrorCategoryValidation, Code: azdext.AddItemUnavailableCode,
				Message: "Enter a project-relative directory without parent traversal.",
			}
		}
		settings["directory"] = directory
	}
	config[id] = settings
	if target < 0 {
		layers = append(layers, map[string]any{"name": providerName, "provider": providerName, "config": config})
	} else {
		layers[target].(map[string]any)["config"] = config
	}
	var updated any = layers
	if path == "layers" {
		if topIndex < 0 {
			projectLayers = append(projectLayers, map[string]any{"name": providerName, "infra": layers})
		} else {
			projectLayers[topIndex].(map[string]any)["infra"] = layers
		}
		updated = projectLayers
	}
	value, err := structpb.NewValue(updated)
	if err != nil {
		return err
	}
	_, err = project.SetConfigValue(ctx, &azdext.SetProjectConfigValueRequest{Path: path, Value: value})
	return err
}

func scanEmulatorEntry(raw any, index int, found *int) error {
	entries, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("layers[%d].infra must be a sequence", index)
	}
	for _, rawEntry := range entries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return fmt.Errorf("layers[%d].infra entries must be mappings", index)
		}
		if entry["provider"] == providerName {
			if *found >= 0 {
				return errors.New("only one emulators provisioning entry is supported")
			}
			*found = index
		}
	}
	return nil
}

func emulatorLayers(infra map[string]any) ([]any, int, error) {
	raw, found := infra["layers"]
	if !found {
		if len(infra) > 0 {
			return nil, -1, errors.New("move existing infrastructure into infra.layers before adding emulators")
		}
		return nil, -1, nil
	}
	layers, ok := raw.([]any)
	if !ok {
		return nil, -1, errors.New("infra.layers must be a sequence")
	}
	target := -1
	nameTaken := false
	for i, rawLayer := range layers {
		layer, ok := rawLayer.(map[string]any)
		if !ok {
			return nil, -1, fmt.Errorf("infra.layers[%d] must be a mapping", i)
		}
		nameTaken = nameTaken || layer["name"] == providerName
		provider := layer["provider"]
		if provider == nil || provider == "" {
			provider = infra["provider"]
		}
		if provider == providerName {
			if target >= 0 {
				return nil, -1, errors.New("only one emulators provisioning entry is supported")
			}
			target = i
		}
	}
	if target < 0 && nameTaken {
		return nil, -1, errors.New("the infrastructure layer name emulators is already in use")
	}
	return layers, target, nil
}
