// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
)

// TestActivityCoexistenceRegression is an end-to-end, offline regression for the
// two ways an agent definition is produced — `azd ai agent init` from local code
// and from a manifest — after Activity was allowed to coexist with other
// protocols. It drives the real production helpers (IsActivityProtocol,
// ComposeActivityAgentEndpoint) and the real schema validation
// (agent_yaml.ValidateAgentDefinition) so a regression in either path is caught
// without needing Azure. Live Teams/bot provisioning is validated separately.
func TestActivityCoexistenceRegression(t *testing.T) {
	// assembleFromCode mirrors init_from_code.go: build the container agent from
	// the selected protocols, then compose the activity endpoint (no-op for
	// non-activity agents). It returns the definition and the marshalled
	// agent.yaml init would validate/write.
	assembleFromCode := func(t *testing.T, protocols []agent_yaml.ProtocolVersionRecord) (agent_yaml.ContainerAgent, []byte) {
		t.Helper()
		def := agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Name: "echo",
				Kind: agent_yaml.AgentKindHosted,
			},
			Protocols: protocols,
			CodeConfiguration: &agent_yaml.CodeConfiguration{
				Runtime:    "python_3_13",
				EntryPoint: "app.py",
			},
		}
		if IsActivityProtocol(def) {
			def.AgentEndpoint = ComposeActivityAgentEndpoint(def.AgentEndpoint, def.Protocols)
		}
		out, err := yaml.Marshal(def)
		require.NoError(t, err)
		// The definition init produces must pass the same schema validation the
		// on-disk agent.yaml path enforces.
		require.NoError(t, agent_yaml.ValidateAgentDefinition(out))
		return def, out
	}

	t.Run("init-from-code", func(t *testing.T) {
		t.Run("responses only is unchanged (no endpoint injected)", func(t *testing.T) {
			def, _ := assembleFromCode(t, []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			})
			require.False(t, IsActivityProtocol(def))
			require.Nil(t, def.AgentEndpoint, "non-activity agents must not carry an agent_endpoint")
		})

		t.Run("activity only keeps the single-protocol bot endpoint", func(t *testing.T) {
			def, _ := assembleFromCode(t, []agent_yaml.ProtocolVersionRecord{
				{Protocol: "activity", Version: "2.0.0"},
			})
			require.True(t, IsActivityProtocol(def))
			require.NotNil(t, def.AgentEndpoint)
			require.Equal(t, []string{"activity"}, def.AgentEndpoint.Protocols)
			requireHasScheme(t, def.AgentEndpoint, "BotServiceRbac")
		})

		t.Run("activity coexists with responses and invocations", func(t *testing.T) {
			def, _ := assembleFromCode(t, []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
				{Protocol: "invocations", Version: "1.0.0"},
				{Protocol: "activity", Version: "2.0.0"},
			})
			require.True(t, IsActivityProtocol(def))
			require.NotNil(t, def.AgentEndpoint)
			// Every selected protocol is advertised on the endpoint, and the
			// bot scheme Activity requires is present alongside them.
			require.Equal(t, []string{"responses", "invocations", "activity"}, def.AgentEndpoint.Protocols)
			requireHasScheme(t, def.AgentEndpoint, "BotServiceRbac")
			// The container still declares all three protocols with versions.
			require.Len(t, def.Protocols, 3)
		})
	})

	t.Run("init-from-manifest", func(t *testing.T) {
		// A manifest-authored coexistence definition is passed through verbatim:
		// azd imposes no activity-exclusive restriction on the manifest path.
		manifest := []byte(`
name: echo
template:
  kind: hosted
  name: echo
  image: myregistry.azurecr.io/echo:v1
  protocols:
    - protocol: responses
      version: 2.0.0
    - protocol: activity
      version: 2.0.0
  agent_endpoint:
    protocols:
      - responses
      - activity
    authorization_schemes:
      - type: Entra
        isolation_key_source:
          kind: Header
      - type: BotServiceRbac
`)
		agent, err := agent_yaml.ExtractAgentDefinition(manifest)
		require.NoError(t, err)
		ca, ok := agent.(agent_yaml.ContainerAgent)
		require.True(t, ok)

		require.True(t, IsActivityProtocol(ca))
		require.Equal(t, ActivityUseCaseSimple, ResolveActivityProfile(ca).UseCase)
		require.Len(t, ca.Protocols, 2)
		require.NotNil(t, ca.AgentEndpoint)
		require.Equal(t, []string{"responses", "activity"}, ca.AgentEndpoint.Protocols)
		requireHasScheme(t, ca.AgentEndpoint, "Entra")
		requireHasScheme(t, ca.AgentEndpoint, "BotServiceRbac")
	})
}

func requireHasScheme(t *testing.T, ep *agent_yaml.AgentEndpoint, schemeType string) {
	t.Helper()
	for _, s := range ep.AuthorizationSchemes {
		if s.Type == schemeType {
			return
		}
	}
	t.Fatalf("expected authorization scheme %q, got %+v", schemeType, ep.AuthorizationSchemes)
}
