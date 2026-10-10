// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/preview"
	"github.com/stretchr/testify/require"
)

func TestProjectServiceTargetPreviewIsSilentNoOp(t *testing.T) {
	provider := newProjectServiceTarget(nil)
	previewer, ok := provider.(preview.ServiceTargetPreviewProvider)
	require.True(t, ok)
	result, err := previewer.Preview(t.Context(), &v1beta.ServiceConfig{Name: "ai-project", Host: aiProjectHost})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.Message)
	require.Equal(t, map[string]any{
		"status": "noOp", "scope": "deployment", "infrastructurePreviewed": false,
	}, result.Data.AsMap())
	target, ok := provider.(*projectServiceTarget)
	require.True(t, ok)
	require.Nil(t, target.serviceConfig, "preview must not initialize or persist service state")

	service := &azdext.ServiceConfig{Name: "ai-project", Host: aiProjectHost}
	require.NoError(t, provider.Initialize(t.Context(), service))
	_, err = previewer.Preview(t.Context(), &v1beta.ServiceConfig{Name: "another-project", Host: aiProjectHost})
	require.NoError(t, err)
	require.Same(t, service, target.serviceConfig, "preview must not change normal deployment state")
	packaged, err := provider.Package(t.Context(), service, nil, nil)
	require.NoError(t, err)
	require.Equal(t, &azdext.ServicePackageResult{}, packaged)
	published, err := provider.Publish(t.Context(), service, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, &azdext.ServicePublishResult{}, published)
	deployed, err := provider.Deploy(t.Context(), service, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, &azdext.ServiceDeployResult{}, deployed)
}

// TestProjectPreviewExtensionProcess exercises the production host registration
// against the unchanged main host in the extension-local integration harness.
func TestProjectPreviewExtensionProcess(t *testing.T) {
	if os.Getenv("AZD_PROJECT_PREVIEW_EXTENSION_PROCESS") != "true" {
		t.Skip("subprocess helper for agents/tests/host-preview/run.ps1")
	}
	ctx, cancel := context.WithTimeout(azdext.WithAccessToken(t.Context()), 30*time.Second)
	defer cancel()
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	defer client.Close()
	host := azdext.NewExtensionHost(client)
	configureExtensionHost(host)
	require.NoError(t, host.Run(ctx))
}
