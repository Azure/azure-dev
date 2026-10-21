// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

func requireProjectPreviewFeatures(
	ctx context.Context, client *azdext.AzdClient, config *azdext.ProjectConfig,
) error {
	environments := make([]map[string]string, 0, len(config.GetServices()))
	for _, service := range config.GetServices() {
		environments = append(environments, service.GetEnvironment())
	}
	for _, service := range config.GetServices() {
		if err := project.RequireAgentFeatures(ctx, client, service, config.GetPath(), environments...); err != nil {
			return err
		}
	}
	return nil
}

func requireInitPreviewFeatures(
	ctx context.Context, client *azdext.AzdClient, flags *initFlags, voiceExplicit bool,
) error {
	harness, err := resolveInitHarness(flags.harness, "")
	if err != nil {
		return err
	}
	if harness != "" {
		if err := featureflags.Require(ctx, client, featureflags.ManagedHarness); err != nil {
			return err
		}
	}
	if strings.EqualFold(strings.TrimSpace(flags.kind), kindFlagPromptVoice) || voiceExplicit {
		return featureflags.Require(ctx, client, featureflags.Voice)
	}
	return nil
}

func requireManifestPreviewFeatures(
	ctx context.Context, client *azdext.AzdClient, content []byte, projectRoot string,
) error {
	var document struct {
		Services map[string]*agentServiceAddRawService `yaml:"services"`
	}
	if err := yaml.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("reading preview features from azure.yaml: %w", err)
	}
	services, err := agentServiceAddDependencyServices(document.Services)
	if err != nil {
		return err
	}
	return requireProjectPreviewFeatures(ctx, client, &azdext.ProjectConfig{Services: services, Path: projectRoot})
}

func requireStagedPreviewFeatures(ctx context.Context, client *azdext.AzdClient, root string) error {
	content, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	if err != nil {
		return err
	}
	return requireManifestPreviewFeatures(ctx, client, content, root)
}

func requireLongRunningFeature(ctx context.Context) error {
	client, err := azdext.NewAzdClient()
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeAzdClientFailed, fmt.Sprintf("connecting to azd for preview opt-in: %s", err),
		)
	}
	defer client.Close()
	return featureflags.Require(ctx, client, featureflags.LongRunning)
}

func requireDefinitionPreviewFeatures(ctx context.Context, definition map[string]any) error {
	if project.PreviewFeature(definition).ID == "" {
		return nil
	}
	client, err := azdext.NewAzdClient()
	if err != nil {
		return err
	}
	defer client.Close()
	return project.RequireDefinitionFeatures(ctx, client, definition)
}

func (a *InvokeAction) requireLocalPreviewFeatures(ctx context.Context) error {
	client, err := azdext.NewAzdClient()
	if err != nil {
		return err
	}
	defer client.Close()
	response, err := client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		st, _ := status.FromError(err)
		if st.Code() == codes.Unknown && st.Message() == azdcontext.ErrNoProject.Error() &&
			len(st.Proto().Details) == 0 {
			return nil
		}
		return err
	}
	if response.GetProject() == nil {
		return fmt.Errorf("loading project for local preview classification: missing project response")
	}
	if len(response.Project.GetServices()) == 0 {
		return nil
	}
	service, config, err := resolveAgentService(ctx, client, a.serviceNameSelector(), a.noPrompt)
	if err != nil {
		return err
	}
	if err := project.RequireAgentFeatures(ctx, client, service, config.GetPath()); err != nil {
		return err
	}
	a.protocolServiceName = service.GetName()
	return nil
}
