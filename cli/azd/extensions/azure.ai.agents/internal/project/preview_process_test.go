// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/require"
)

// TestPreviewExtensionProcess is launched by the extension-local host integration
// harness. It uses the published SDK against the unchanged main host.
func TestPreviewExtensionProcess(t *testing.T) {
	if os.Getenv("AZD_PREVIEW_EXTENSION_PROCESS") != "true" {
		t.Skip("subprocess helper for tests/host-preview/run.ps1")
	}
	ctx, cancel := context.WithTimeout(azdext.WithAccessToken(t.Context()), 30*time.Second)
	defer cancel()
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	defer client.Close()
	host := azdext.NewExtensionHost(client).WithBetaServiceTargetPreview(
		foundryAgentHost, func() azdext.ServiceTargetProvider {
			provider := NewAgentServiceTargetProvider(client).(*AgentServiceTargetProvider)
			provider.previewReader = func(string, string) (agentPreviewReader, error) {
				return &recordingPreviewReader{agent: remotePreviewAgent(previewRequest(t))}, nil
			}
			return &previewProcessProvider{AgentServiceTargetProvider: provider, t: t}
		})
	// The parent ends the process after the host assertions; no Azure credential is used.
	require.NoError(t, host.Run(ctx))
}

type previewProcessProvider struct {
	*AgentServiceTargetProvider
	t *testing.T
}

func (p *previewProcessProvider) Initialize(context.Context, *azdext.ServiceConfig) error {
	p.t.Fatal("preview invoked Initialize")
	return nil
}

func (p *previewProcessProvider) Preview(
	ctx context.Context, service *v1beta.ServiceConfig,
) (*v1beta.ServiceDeployPreviewResult, error) {
	require.Nil(p.t, p.serviceConfig)
	require.False(p.t, p.deployContextReady)
	if service.GetName() == "config-update" || service.GetName() == "protected-update" {
		p.previewReader = func(string, string) (agentPreviewReader, error) {
			request := previewRequest(p.t)
			request.Description = new("A basic responses agent.")
			request.Metadata["tags"] = `["responses","removed"]`
			if service.GetName() == "protected-update" {
				request.Description = new("Old private-previous-secret instructions.")
				hosted, ok := request.Definition.(agent_api.HostedAgentDefinition)
				require.True(p.t, ok)
				hosted.EnvironmentVariables = map[string]string{"API_KEY": "private-previous-secret"}
				request.Definition = hosted
			}
			return &recordingPreviewReader{agent: remotePreviewAgent(request)}, nil
		}
	} else if service.GetName() == "create" {
		p.previewReader = func(string, string) (agentPreviewReader, error) {
			return &recordingPreviewReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}, nil
		}
	} else if service.GetName() == "code" {
		p.previewReader = func(string, string) (agentPreviewReader, error) {
			request := previewRequest(p.t)
			hosted := request.Definition.(agent_api.HostedAgentDefinition)
			hosted.CodeConfiguration = &agent_api.CodeConfigurationAPI{
				Runtime: "python_3_12", EntryPoint: []string{"python", "old.py"}, DependencyResolution: "bundled",
			}
			request.Definition = hosted
			return &recordingPreviewReader{agent: remotePreviewAgent(request)}, nil
		}
	} else if strings.HasPrefix(service.GetName(), "tags-") &&
		service.GetName() != "tags-add" && service.GetName() != "tags-safe" {
		p.previewReader = func(string, string) (agentPreviewReader, error) {
			request := previewRequest(p.t)
			request.Metadata["tags"] = `["retained","removed"]`
			return &recordingPreviewReader{agent: remotePreviewAgent(request)}, nil
		}
	}
	return p.AgentServiceTargetProvider.Preview(ctx, service)
}
