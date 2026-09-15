// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type unadvertisedPreviewTarget struct {
	project.ServiceTarget
}

type advertisedPreviewTarget struct {
	deployPreviewTarget
}

func (*advertisedPreviewTarget) SupportsPreview() bool { return true }

func (*unadvertisedPreviewTarget) SupportsPreview() bool { return false }

func (*unadvertisedPreviewTarget) Preview(
	context.Context, *project.ServiceConfig,
) (*project.ServiceDeployPreviewResult, error) {
	panic("preview must not be dispatched when the capability is absent")
}

func TestDeployPreviewSkipsUnsupportedTargetsBeforeDispatch(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target project.ServiceTarget
	}{
		{name: "no-preview-interface", target: &deployWithoutPreviewTarget{}},
		{name: "unadvertised-capability", target: &unadvertisedPreviewTarget{}},
		{
			name: "older-extension",
			target: project.NewExternalServiceTarget(
				"azure.ai.project", project.ServiceTargetKind("azure.ai.project"), nil, nil, nil, nil, nil,
			),
		},
	} {
		for _, format := range []struct {
			name      string
			formatter output.Formatter
		}{
			{name: "text", formatter: &output.NoneFormatter{}},
			{name: "json", formatter: &output.JsonFormatter{}},
		} {
			t.Run(tt.name+"/"+format.name, func(t *testing.T) {
				action, manager, writer := newDeployPreviewAction(t, "--all")
				action.formatter = format.formatter
				service := action.projectConfig.Services["api"]
				service.Host = project.ServiceTargetKind("azure.ai.project")
				manager.On("GetServiceTarget", mock.Anything, service).Return(tt.target, nil).Once()
				expectDeployPreview(t, action, manager, "web", &project.ServiceDeployPreviewResult{
					Message: "Agent configuration changes",
					Data:    map[string]any{"hasChanges": true},
				})

				_, err := action.Run(t.Context())
				require.NoError(t, err)
				if format.formatter.Kind() == output.JsonFormat {
					var result DeploymentPreviewResult
					require.NoError(t, json.Unmarshal(writer.Bytes(), &result))
					require.Len(t, result.Services, 1)
					require.Contains(t, result.Services, "web")
					require.NotContains(t, result.Services, "api")
					require.Equal(t, map[string]string{"api": "azure.ai.project"}, result.SkippedServices)
				} else {
					require.Equal(t, "Agent configuration changes\n", writer.String())
					require.NotContains(t, writer.String(), "Foundry project")
				}
			})
		}
	}
}

func TestDeployPreviewDispatchesAdvertisedTargets(t *testing.T) {
	action, manager, writer := newDeployPreviewAction(t, "web")
	service := action.projectConfig.Services["web"]
	target := &advertisedPreviewTarget{}
	target.Test(t)
	manager.On("GetServiceTarget", mock.Anything, service).Return(target, nil).Once()
	target.On("Preview", mock.Anything, service).Return(&project.ServiceDeployPreviewResult{
		Message: "Agent configuration changes",
		Data:    map[string]any{"hasChanges": true},
	}, nil).Once()

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t, "Agent configuration changes\n", writer.String())
	target.AssertExpectations(t)
}

func TestDeployPreviewAllUnsupportedIsNotSuccess(t *testing.T) {
	action, manager, writer := newDeployPreviewAction(t, "--all")
	for _, name := range []string{"api", "web"} {
		manager.On("GetServiceTarget", mock.Anything, action.projectConfig.Services[name]).
			Return(&unadvertisedPreviewTarget{}, nil).Once()
	}
	_, err := action.Run(t.Context())
	require.ErrorContains(t, err, "no selected service could be previewed")
	require.Empty(t, writer.String())
}

func TestDeployPreviewDoesNotSkipProviderErrors(t *testing.T) {
	action, manager, writer := newDeployPreviewAction(t, "--all")
	manager.On("GetServiceTarget", mock.Anything, action.projectConfig.Services["api"]).
		Return(&unadvertisedPreviewTarget{}, nil).Once()
	service := action.projectConfig.Services["web"]
	target := &deployPreviewTarget{}
	// A provider error mentioning unsupported functionality must not be confused
	// with core's pre-dispatch capability check.
	expected := errors.New("remote service does not support this operation")
	manager.On("GetServiceTarget", mock.Anything, service).Return(target, nil).Once()
	target.On("Preview", mock.Anything, service).Return(nil, expected).Once()
	_, err := action.Run(t.Context())
	require.ErrorIs(t, err, expected)
	require.Empty(t, writer.String())
	target.AssertExpectations(t)
}
