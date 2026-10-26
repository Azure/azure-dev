// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostdownOwnedProjectClearsAllRemoteVersionsOnly(t *testing.T) {
	t.Parallel()
	const (
		endpoint  = "https://acct.services.ai.azure.com/api/projects/project"
		base      = "acct.services.ai.azure.com/api/projects/project/agents/echo"
		projectID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/" +
			"providers/Microsoft.CognitiveServices/accounts/acct/projects/project"
	)
	for _, test := range []struct {
		name                                     string
		complete, projectDeleted, accountDeleted bool
		recordedEndpoint                         string
		wantDeleted, wantError                   bool
	}{
		{"completed", true, true, true, endpoint, true, false},
		{"failed-down", false, false, false, endpoint, false, false},
		{"partial-child-delete", false, true, false, endpoint, false, false},
		{"unconfirmed-project", true, false, true, endpoint, false, true},
		{"unconfirmed-account", true, true, false, endpoint, false, true},
		{"missing-endpoint-proof", true, true, true, "", false, true},
		{"other-project", true, true, true, endpoint + "-other", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			journal, err := json.Marshal(map[string]any{
				"version": 2, "cleanupComplete": test.complete,
				"foundry": map[string]any{
					"spec":            map[string]any{"projectId": projectID},
					"projectEndpoint": test.recordedEndpoint,
					"projectDeleted":  test.projectDeleted, "accountDeleted": test.accountDeleted,
				},
			})
			require.NoError(t, err)
			env := &testEnvironmentServiceServer{values: map[string]map[string]string{"dev": {
				"AZD_FOUNDRY_BYO_AKS_PREPARATION": string(journal),
				"AGENT_ECHO_ENDPOINT":             endpoint + "/agents/echo/versions/2",
			}}}
			env.current = &azdext.Environment{Name: "dev"}
			client := newInvokeTestAzdClient(t, newInvokeUserConfigServer(), env)
			owned := []string{base, base + "/remote", base + "/versions/1/remote", base + "/versions/2/remote"}
			others := []string{
				base + "-other/versions/1/remote",
				base + "/versions/1/local",
				"acct.services.ai.azure.com/api/projects/other/agents/echo/versions/1/remote",
				"localhost:8080/agents/echo",
			}
			for _, key := range append(owned, others...) {
				saveAgentStateForTest(t, client, key, agentStateRecord{
					SessionID: "session", ConversationID: "conversation", StateStore: "checkpoints",
				})
			}
			err = postdownHandler(t.Context(), client, &azdext.ProjectEventArgs{
				Project: &azdext.ProjectConfig{Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"echo": {Name: "echo", Host: AiAgentHost},
				}},
			})
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			for _, key := range owned {
				state := loadAgentStateForTest(t, client, key)
				if test.wantDeleted {
					assert.Nil(t, state, key)
				} else {
					require.NotNil(t, state, key)
					assert.Equal(t, "session", state.SessionID)
					assert.Equal(t, "conversation", state.ConversationID)
				}
			}
			for _, key := range others {
				require.NotNil(t, loadAgentStateForTest(t, client, key), key)
			}
		})
	}
}

func TestDeleteRemoteVersionsRejectsInvalidInventoryBeforeDeleting(t *testing.T) {
	t.Parallel()
	const base = "acct.services.ai.azure.com/api/projects/project/agents/echo"
	client := newInvokeTestAzdClient(t, newInvokeUserConfigServer())
	store, err := newAgentStateStoreForClient(client)
	require.NoError(t, err)
	saveAgentStateForTest(t, client, base+"/versions/1/remote", agentStateRecord{SessionID: "v1"})
	saveAgentStateForTest(t, client, base+"/versions/2/remote", agentStateRecord{SessionID: "v2"})
	path := store.statePath(base + "/versions/1/remote")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	require.ErrorContains(t, store.deleteRemoteVersions(t.Context(), base+"/versions/2/remote"), "inventory")
	require.NotNil(t, loadAgentStateForTest(t, client, base+"/versions/2/remote"))
	require.NoError(t, os.WriteFile(path, raw, 0o600)) //nolint:gosec // Test path is a hashed key under t.TempDir().
	require.NoError(t, store.deleteRemoteVersions(t.Context(), base+"/versions/2/remote"))
	require.Nil(t, loadAgentStateForTest(t, client, base+"/versions/1/remote"))
	require.Nil(t, loadAgentStateForTest(t, client, base+"/versions/2/remote"))
}

func TestPostdownWithoutOwnedProjectKeepsVersionScopedCleanup(t *testing.T) {
	t.Parallel()
	const base = "acct.services.ai.azure.com/api/projects/project/agents/echo"
	env := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "dev"},
		values: map[string]map[string]string{"dev": {
			"AGENT_ECHO_ENDPOINT": "https://" + base + "/versions/2",
		}},
	}
	client := newInvokeTestAzdClient(t, newInvokeUserConfigServer(), env)
	for _, key := range []string{base, base + "/versions/1/remote", base + "/versions/2/remote"} {
		saveAgentStateForTest(t, client, key, agentStateRecord{SessionID: "saved"})
	}
	require.NoError(t, postdownHandler(t.Context(), client, &azdext.ProjectEventArgs{
		Project: &azdext.ProjectConfig{Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
			"echo": {Name: "echo", Host: AiAgentHost},
		}},
	}))
	require.NotNil(t, loadAgentStateForTest(t, client, base+"/versions/1/remote"))
	require.Nil(t, loadAgentStateForTest(t, client, base+"/versions/2/remote"))
	require.Nil(t, loadAgentStateForTest(t, client, base))
}

func TestDownStateEndpointCredentialsAreNotDisclosed(t *testing.T) {
	t.Parallel()
	_, err := stateTargetFromDeployedEndpoint(
		"https://user:private-password@acct.services.ai.azure.com/api/projects/p/agents/a/versions/1?sig=private-signature",
	)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private-password")
	assert.NotContains(t, err.Error(), "private-signature")
}
