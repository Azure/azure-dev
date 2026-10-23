// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/recordproxy"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type previewProjectServer struct {
	azdext.UnimplementedProjectServiceServer
}

func (*previewProjectServer) Get(context.Context, *azdext.EmptyRequest) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: &azdext.ProjectConfig{}}, nil
}

func TestPreviewNestedHarnessReference(t *testing.T) {
	t.Setenv(featureflags.ManagedHarness.EnvVar, "")
	t.Setenv("AZD_ENVIRONMENT", "")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "harness.yaml"),
		[]byte("type: microsoft_copilot_preview\n"), 0600))
	service := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind": "prompt", "harness": map[string]any{"$ref": "harness.yaml"},
		})}
	before := proto.CloneOf(service)
	require.ErrorContains(t, RequireAgentFeatures(service, root), "agents.managed_harness")
	provider := &AgentServiceTargetProvider{projectPath: root}
	_, err := provider.Package(t.Context(), service, nil, nil)
	require.ErrorContains(t, err, "agents.managed_harness")
	require.True(t, proto.Equal(before, service))
}

func TestPreviewFeatureRuntimeBoundaries(t *testing.T) {
	for _, feature := range featureflags.All() {
		t.Setenv(feature.EnvVar, "")
	}
	t.Setenv("AZD_ENVIRONMENT", "")
	resourceID, err := arm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project")
	require.NoError(t, err)
	originalTransport := recordproxy.Transport
	t.Cleanup(func() { recordproxy.Transport = originalTransport })
	recordproxy.Transport = hostedResourceErrorTransport(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, resourceID.String(), request.URL.Path)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"name":"project","location":"eastus"}`)),
			Request:    request,
		}, nil
	})
	for _, tc := range []struct {
		name       string
		definition map[string]any
		feature    featureflags.Feature
	}{
		{"ordinary hosted", map[string]any{"kind": "hosted"}, featureflags.Feature{}},
		{"ordinary prompt", map[string]any{"kind": "prompt"}, featureflags.Feature{}},
		{"managed harness", map[string]any{"kind": "prompt",
			"harness": map[string]any{"type": "microsoft_copilot_preview"}}, featureflags.ManagedHarness},
		{"legacy harness", map[string]any{"kind": "prompt",
			"harness": map[string]any{"type": "github_copilot_preview"}}, featureflags.ManagedHarness},
		{"managed harness with whitespace", map[string]any{
			"kind": "prompt", "name": "agent", "model": "gpt-4.1-mini", "instructions": "Be helpful.",
			"harness": map[string]any{"type": " \tmicrosoft_copilot_preview\r\n "},
		}, featureflags.ManagedHarness},
		{"legacy harness with whitespace", map[string]any{
			"kind": "prompt", "name": "agent", "model": "gpt-4.1-mini", "instructions": "Be helpful.",
			"harness": map[string]any{"type": " \tgithub_copilot_preview\r\n "},
		}, featureflags.ManagedHarness},
		{"voice", map[string]any{"kind": "voice"}, featureflags.Voice},
		{"prompt voice", map[string]any{"kind": "prompt-voice"}, featureflags.Voice},
		{"hosted voice bridge", map[string]any{"kind": "hosted",
			"protocols": []any{map[string]any{"protocol": "invocations_ws"}}}, featureflags.Voice},
		{"api hosted voice bridge", map[string]any{"kind": "hosted",
			"protocol_versions": []any{map[string]any{"protocol": "invocations_ws"}}}, featureflags.Voice},
		{"legacy api hosted voice bridge", map[string]any{"kind": "hosted",
			"container_protocol_versions": []any{map[string]any{"protocol": " invocations_ws "}}}, featureflags.Voice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			props, err := structpb.NewStruct(tc.definition)
			require.NoError(t, err)
			for _, ref := range []bool{false, true} {
				t.Run(map[bool]string{false: "inline", true: "ref"}[ref], func(t *testing.T) {
					service := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, AdditionalProperties: props}
					if ref {
						content, err := json.Marshal(tc.definition)
						require.NoError(t, err)
						require.NoError(t, os.WriteFile(filepath.Join(root, "agent.json"), content, 0600))
						service.AdditionalProperties = mustStruct(t, map[string]any{"$ref": "agent.json"})
					}
					before := proto.CloneOf(service)
					ctx := t.Context()
					err := RequireAgentFeatures(service, root)
					if tc.feature.ID == "" {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, tc.feature.ID)
						require.Contains(t, err.Error(), "disabled")
						provider := &AgentServiceTargetProvider{
							projectPath: root, deployContextReady: true,
							foundryProject: resourceID, credential: hostedResourceErrorCredential{},
						}
						_, err = provider.Package(ctx, proto.CloneOf(service), nil, nil)
						require.ErrorContains(t, err, tc.feature.ID)
						_, err = provider.Deploy(ctx, proto.CloneOf(service), nil, nil, nil)
						require.ErrorContains(t, err, tc.feature.ID)
						_, err = provider.Publish(ctx, proto.CloneOf(service), nil, nil, nil, nil)
						require.ErrorContains(t, err, tc.feature.ID)
						target, err := provider.GetTargetResource(ctx, "sub", proto.CloneOf(service), nil)
						require.NoError(t, err, "target reads must work with previews off")
						require.Equal(t, "sub", target.SubscriptionId)
						if tc.definition["kind"] != "prompt" {
							require.Equal(t, "project", target.ResourceName)
							require.Equal(t, "eastus", target.Metadata["location"])
						}
						t.Setenv(tc.feature.EnvVar, "true")
						require.NoError(t, RequireAgentFeatures(service, root))
						t.Setenv(tc.feature.EnvVar, "")
					}
					require.True(t, proto.Equal(before, service))
				})
			}
		})
	}
}
