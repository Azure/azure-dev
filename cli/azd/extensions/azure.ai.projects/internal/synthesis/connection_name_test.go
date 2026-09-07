// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionSynthesisUsesPayloadNameWithServiceKeyFallback(t *testing.T) {
	t.Parallel()
	const serviceKey = "connection-service"
	for _, test := range []struct {
		name   string
		fields string
		want   string
	}{
		{name: "omitted", want: serviceKey},
		{name: "empty", fields: "    name: ''\n", want: serviceKey},
		{name: "blank", fields: "    name: '   '\n", want: serviceKey},
		{name: "explicit", fields: "    name: '  Private Registry  '\n", want: "Private Registry"},
		{name: "reference", fields: "    $ref: ./connection.yaml\n", want: "Referenced Connection"},
		{
			name: "reference overlay", fields: "    $ref: ./connection.yaml\n    name: Override\n",
			want: "Override",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "connection.yaml"),
				[]byte("name: '  Referenced Connection  '\n"), 0o600))
			for _, existing := range []bool{false, true} {
				for _, preserve := range []bool{false, true} {
					t.Run(fmt.Sprintf("existing=%t/preserve=%t", existing, preserve), func(t *testing.T) {
						endpoint := ""
						if existing {
							endpoint = "    endpoint: https://account.services.ai.azure.com/api/projects/project\n"
						}
						raw := []byte("services:\n  project:\n    host: azure.ai.project\n" + endpoint +
							"  connection-service:\n    host: azure.ai.connection\n" + test.fields +
							"    category: RemoteTool\n    authType: ApiKey\n    target: ${URL}\n" +
							"    credentials: {key: '${KEY}'}\n    env: {}\n")
						input := Input{
							RawAzureYAML: raw, ServiceName: "project", ProjectRoot: root, PreserveVarRefs: preserve,
							Env: map[string]string{"URL": "wrong-project", "KEY": "wrong-project"},
							ServiceEnvironments: map[string]map[string]string{
								serviceKey: {"URL": "https://service.example", "KEY": "service-secret"},
							},
						}
						// Service scopes must remain indexed by service key, not resource name.
						if test.want != serviceKey {
							input.ServiceEnvironments[test.want] = map[string]string{"KEY": "wrong-service"}
						}
						synthesize := Synthesize
						if existing {
							synthesize = SynthesizeExistingProject
						}
						result, err := synthesize(input)
						require.NoError(t, err)
						connections, ok := result.Parameters["connections"].([]Connection)
						require.True(t, ok)
						require.Len(t, connections, 1)
						assert.Equal(t, test.want, connections[0].Name)
						assert.Nil(t, connections[0].Credentials)
						credentials, ok := result.Parameters["connectionCredentials"].(map[string]map[string]any)
						require.True(t, ok)
						target, secret := "https://service.example", "service-secret" //nolint:gosec // synthetic test values
						if preserve {
							target, secret = "${URL}", "${KEY}"
						}
						assert.Equal(t, target, connections[0].Target)
						assert.Equal(t, map[string]map[string]any{test.want: {"key": secret}}, credentials)
						joined := JoinConnectionCredentials(connections, credentials)
						assert.Equal(t, test.want, joined[0].Name)
						assert.Equal(t, secret, joined[0].Credentials["key"])
						scopes, err := ConnectionEnvironmentScopes(raw, root, nil)
						require.NoError(t, err)
						assert.Equal(t, map[string]string{test.want: serviceKey}, scopes)
					})
				}
			}
		})
	}
}

func TestConnectionSynthesisSortsByResolvedName(t *testing.T) {
	t.Parallel()
	result, err := Synthesize(Input{
		RawAzureYAML: []byte(`services:
  project:
    host: azure.ai.project
  first-service:
    host: azure.ai.connection
    name: Zulu
  last-service:
    host: azure.ai.connection
    name: Alpha
`),
		ServiceName: "project",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Alpha", "Zulu"}, resultConnectionNames(t, result))
}
