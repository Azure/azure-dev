// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

// previewServiceTarget only implements Preview; any other lifecycle call panics on the nil embedded interface.
type previewServiceTarget struct {
	project.ServiceTarget
	result *project.ServiceDeployPreviewResult
	err    error
}

func (t *previewServiceTarget) Preview(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
) (*project.ServiceDeployPreviewResult, error) {
	return t.result, t.err
}

type previewServiceManager struct {
	mockDeployServiceManager
	targets   map[string]project.ServiceTarget
	resources map[string]*environment.TargetResource
}

func (m *previewServiceManager) GetServiceTarget(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
) (project.ServiceTarget, error) {
	return m.targets[serviceConfig.Name], nil
}

func (m *previewServiceManager) GetTargetResource(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
	serviceTarget project.ServiceTarget,
) (*environment.TargetResource, error) {
	return m.resources[serviceConfig.Name], nil
}

func newDeployPreviewAction(
	t *testing.T,
	formatter output.Formatter,
	agentTarget *previewServiceTarget,
	args ...string,
) (*DeployAction, *bytes.Buffer) {
	t.Helper()

	projectConfig, err := project.Parse(t.Context(), "name: test-proj\nservices:\n"+
		"  agent:\n    project: src/agent\n    host: azure.ai.agent\n"+
		"  api:\n    project: src/api\n    language: js\n    host: containerapp\n")
	require.NoError(t, err)

	cmd := NewDeployCmd()
	flags := NewDeployFlags(cmd, &internal.GlobalCommandOptions{})
	require.NoError(t, cmd.ParseFlags(append([]string{"--preview"}, args...)))

	env := environment.New("test-env")
	env.SetSubscriptionId("subscription-id")
	writer := &bytes.Buffer{}

	// The project manager mock has no expectations, so service initialization would fail the test.
	return &DeployAction{
		flags:          flags,
		args:           cmd.Flags().Args(),
		projectConfig:  projectConfig,
		env:            env,
		importManager:  project.NewImportManager(nil),
		projectManager: &mockDeployProjectManager{},
		serviceManager: &previewServiceManager{
			targets: map[string]project.ServiceTarget{
				"agent": agentTarget,
				"api":   &mockServiceTargetWithoutPreview{},
			},
			resources: map[string]*environment.TargetResource{},
		},
		console:             mockinput.NewMockConsole(),
		formatter:           formatter,
		writer:              writer,
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}, writer
}

type mockServiceTargetWithoutPreview struct {
	project.ServiceTarget
}

type resourcePreviewServiceTarget struct {
	project.ServiceTarget
	targetResource *environment.TargetResource
	err            error
}

func (t *resourcePreviewServiceTarget) PreviewWithTarget(
	ctx context.Context,
	serviceConfig *project.ServiceConfig,
	targetResource *environment.TargetResource,
) (*project.ServiceDeployPreviewResult, error) {
	t.targetResource = targetResource
	if t.err != nil {
		return nil, t.err
	}
	return &project.ServiceDeployPreviewResult{Message: "resource preview"}, nil
}

func TestDeployActionPreview(t *testing.T) {
	t.Parallel()

	agentTarget := &previewServiceTarget{
		result: &project.ServiceDeployPreviewResult{Message: "agent: 1 change", Data: map[string]any{"action": "update"}},
	}
	action, _ := newDeployPreviewAction(t, &output.NoneFormatter{}, agentTarget, "--all")

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Contains(t, result.Message.Header, "Generated deployment preview")

	consoleOutput := strings.Join(action.console.(*mockinput.MockConsole).Output(), "\n")
	require.Contains(t, consoleOutput, "agent: 1 change")
	require.Contains(t, consoleOutput, "Service 'api' (host: containerapp) does not support deployment preview.")
}

func TestDeployActionPreviewJson(t *testing.T) {
	t.Parallel()

	agentTarget := &previewServiceTarget{
		result: &project.ServiceDeployPreviewResult{Message: "agent: 1 change", Data: map[string]any{"action": "update"}},
	}
	action, writer := newDeployPreviewAction(t, &output.JsonFormatter{}, agentTarget, "--all")

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	var parsed DeploymentPreviewResult
	require.NoError(t, json.Unmarshal(writer.Bytes(), &parsed))
	require.Equal(t, map[string]*project.ServiceDeployPreviewResult{
		"agent": {Message: "agent: 1 change", Data: map[string]any{"action": "update"}},
	}, parsed.Services)
}

func TestDeployActionPreviewResolvesTargetForBuiltInPreview(t *testing.T) {
	t.Parallel()

	action, _ := newDeployPreviewAction(t, &output.NoneFormatter{}, &previewServiceTarget{}, "api")
	previewTarget := &resourcePreviewServiceTarget{}
	targetResource := environment.NewTargetResource(
		"subscription-id",
		"resource-group",
		"container-app",
		"Microsoft.App/containerApps",
	)
	serviceManager := action.serviceManager.(*previewServiceManager)
	serviceManager.targets["api"] = previewTarget
	serviceManager.resources["api"] = targetResource

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Same(t, targetResource, previewTarget.targetResource)
}

func TestDeployActionPreviewReturnsBuiltInPreviewError(t *testing.T) {
	t.Parallel()

	action, _ := newDeployPreviewAction(t, &output.NoneFormatter{}, &previewServiceTarget{}, "api")
	serviceManager := action.serviceManager.(*previewServiceManager)
	serviceManager.targets["api"] = &resourcePreviewServiceTarget{err: errors.New("preview failed")}
	serviceManager.resources["api"] = environment.NewTargetResource(
		"subscription-id",
		"resource-group",
		"container-app",
		"Microsoft.App/containerApps",
	)

	_, err := action.Run(t.Context())
	require.EqualError(t, err, "previewing service 'api': preview failed")
}

func TestDeployActionPreviewErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    *previewServiceTarget
		args      []string
		wantError string
		wantIs    error
	}{
		{
			name:      "PreviewFails",
			target:    &previewServiceTarget{err: errors.New("remote lookup failed")},
			args:      []string{"agent"},
			wantError: "previewing service 'agent': remote lookup failed",
		},
		{
			name:      "NilResult",
			target:    &previewServiceTarget{},
			args:      []string{"agent"},
			wantError: "previewing service 'agent': service target returned no deployment preview",
		},
		{
			name:   "WithTimeout",
			target: &previewServiceTarget{},
			args:   []string{"--all", "--timeout", "30"},
			wantIs: internal.ErrInvalidFlagCombination,
		},
		{
			name:   "WithFromPackage",
			target: &previewServiceTarget{},
			args:   []string{"agent", "--from-package", "image:tag"},
			wantIs: internal.ErrInvalidFlagCombination,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			action, _ := newDeployPreviewAction(t, &output.NoneFormatter{}, tt.target, tt.args...)
			_, err := action.Run(t.Context())
			if tt.wantIs != nil {
				require.ErrorIs(t, err, tt.wantIs)
				return
			}
			require.EqualError(t, err, tt.wantError)
		})
	}
}
