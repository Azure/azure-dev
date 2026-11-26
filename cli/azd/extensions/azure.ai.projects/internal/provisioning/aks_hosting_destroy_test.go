// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS skiptoken

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hostingDestroyGroupID = "/subscriptions/sub/resourceGroups/foundry"
const hostingDestroyAccountA = hostingDestroyGroupID + "/providers/Microsoft.CognitiveServices/accounts/custom-account-a"
const hostingDestroyAccountB = hostingDestroyGroupID + "/providers/Microsoft.CognitiveServices/accounts/custom-account-b"
const hostingDestroyAccountC = hostingDestroyGroupID + "/providers/Microsoft.CognitiveServices/accounts/ordinary-account"
const hostingDestroyProjectA = hostingDestroyAccountA + "/projects/custom-project-a"
const hostingDestroyProjectB = hostingDestroyAccountA + "/projects/custom-project-b"
const hostingDestroyIdentity = "/subscriptions/SUB/resourceGroups/FOUNDRY/" +
	"providers/Microsoft.ManagedIdentity/userAssignedIdentities/custom-hosting"
const hostingDestroyExternalIdentity = "/subscriptions/sub/resourceGroups/external/" +
	"providers/Microsoft.ManagedIdentity/userAssignedIdentities/supplied-hosting"
const hostingDestroyPurgeKey = "extensions.ai-projects.accountPurge"

type hostingDestroyRecoveryFixture struct {
	env                    *resolveEnvStubEnvServer
	live, softDeleted      map[string]bool
	projectGone, groupGone bool
	failAt, failureMode    string
	lookupMode             string
	ordinary, layer        bool
	onRequest              func(string)
	events, purged         []string
}

func newHostingDestroyRecoveryFixture() *hostingDestroyRecoveryFixture {
	return &hostingDestroyRecoveryFixture{
		env: &resolveEnvStubEnvServer{envName: "test", get: map[string]string{
			envKeySubscriptionID: "sub", envKeyLocation: "eastus2", envKeyResourceGroup: "foundry",
		}},
		live:        map[string]bool{hostingDestroyAccountA: true, hostingDestroyAccountB: true},
		softDeleted: map[string]bool{},
	}
}

func (f *hostingDestroyRecoveryFixture) provider(t *testing.T) *FoundryProvisioningProvider {
	t.Helper()
	p := &FoundryProvisioningProvider{
		azdClient: newResolveEnvTestClient(t, f.env, &resolveEnvStubPromptServer{}),
		envName:   "test", credential: acrTestCredential{}, isLayer: f.layer,
	}
	if f.layer {
		p.infraName = "foundry"
	}
	require.NoError(t, p.resolveEnv(t.Context()))
	require.NoError(t, p.loadACRLifecycle(t.Context()))
	p.synthesisEnv = maps.Clone(f.env.get)
	deploymentID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Retry: policy.RetryOptions{MaxRetries: -1},
		Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
			path := request.URL.Path
			if strings.EqualFold(path, hostingDestroyGroupID) {
				path = hostingDestroyGroupID
			}
			event := request.Method + " " + path
			f.events = append(f.events, event)
			if f.onRequest != nil {
				f.onRequest(event)
			}
			reply := func(code int, body any) (*http.Response, error) {
				return acrTestHTTPReply(t, request, code, body)
			}
			missing := func() (*http.Response, error) {
				return reply(http.StatusNotFound, map[string]any{
					"error": map[string]any{"code": "ResourceNotFound", "message": "resource is absent"},
				})
			}
			if event == f.failAt {
				switch f.failureMode {
				case "canceled":
					return nil, context.Canceled
				case "deadline":
					return nil, context.DeadlineExceeded
				case "notfound":
					return missing()
				default:
					return reply(http.StatusForbidden, map[string]any{
						"error": map[string]any{"code": "AuthorizationFailed", "message": "cleanup denied"},
					})
				}
			}
			switch {
			case strings.EqualFold(path, hostingDestroyGroupID):
				if f.groupGone {
					return missing()
				}
				if request.Method == http.MethodGet {
					return reply(http.StatusOK, map[string]any{"tags": map[string]any{"azd-env-name": "test"}})
				}
				require.Equal(t, http.MethodDelete, request.Method)
				f.groupGone = true
				for id, live := range f.live {
					if live {
						f.softDeleted[id], f.live[id] = true, false
					}
				}
				return reply(http.StatusOK, nil)
			case path == deploymentID:
				if request.Method == http.MethodGet {
					return missing()
				}
				require.Equal(t, http.MethodDelete, request.Method)
				require.True(t, f.groupGone)
				return reply(http.StatusNoContent, nil)
			case path == hostingDestroyGroupID+"/providers/Microsoft.CognitiveServices/accounts":
				require.Equal(t, http.MethodGet, request.Method)
				if f.groupGone {
					return missing()
				}
				var accounts []any
				for _, id := range []string{hostingDestroyAccountA, hostingDestroyAccountB, hostingDestroyAccountC} {
					if !f.live[id] {
						continue
					}
					location := "eastus2"
					if id == hostingDestroyAccountB {
						location = "westus2"
					} else if id == hostingDestroyAccountC {
						location = "centralus"
					}
					account := map[string]any{
						"id": id, "name": id[strings.LastIndex(id, "/")+1:], "location": location, "kind": "AIServices",
					}
					if id != hostingDestroyAccountC {
						account["identity"] = map[string]any{"userAssignedIdentities": map[string]any{hostingDestroyIdentity: map[string]any{}}}
					}
					accounts = append(accounts, account)
				}
				return reply(http.StatusOK, map[string]any{"value": accounts})
			case path == hostingDestroyAccountA || path == hostingDestroyAccountB:
				if !f.live[path] {
					return missing()
				}
				if request.Method == http.MethodGet {
					var configs []any
					if !f.ordinary {
						configs = []any{map[string]any{
							"hostingType": "ManagedCluster", "hostingManagementIdentityResourceId": hostingDestroyIdentity,
						}}
					}
					return reply(http.StatusOK, map[string]any{"properties": map[string]any{
						"agentHostingConfigurations": configs,
					}})
				}
				require.Equal(t, http.MethodDelete, request.Method)
				if path == hostingDestroyAccountA {
					require.True(t, f.projectGone)
				}
				f.live[path], f.softDeleted[path] = false, true
				return reply(http.StatusOK, nil)
			case path == hostingDestroyProjectA:
				require.Equal(t, http.MethodDelete, request.Method)
				f.projectGone = true
				return reply(http.StatusOK, nil)
			case path == hostingDestroyAccountA+"/projects":
				require.Equal(t, http.MethodGet, request.Method)
				var projects []any
				if !f.projectGone {
					projects = []any{map[string]any{"id": hostingDestroyProjectA}}
				}
				return reply(http.StatusOK, map[string]any{"value": projects})
			case path == hostingDestroyAccountB+"/projects" ||
				path == hostingDestroyAccountA+"/deployments" || path == hostingDestroyAccountB+"/deployments" ||
				path == hostingDestroyAccountC+"/deployments":
				require.Equal(t, http.MethodGet, request.Method)
				return reply(http.StatusOK, map[string]any{"value": []any{}})
			case strings.Contains(path, "/deletedAccounts/"):
				require.True(t, f.groupGone)
				id := hostingDestroyAccountA
				location := "eastus2"
				if strings.HasSuffix(path, "/custom-account-b") {
					id, location = hostingDestroyAccountB, "westus2"
				} else if strings.HasSuffix(path, "/ordinary-account") {
					id, location = hostingDestroyAccountC, "centralus"
				}
				require.Equal(t, "/subscriptions/sub/providers/Microsoft.CognitiveServices/locations/"+location+
					"/resourceGroups/foundry/deletedAccounts/"+id[strings.LastIndex(id, "/")+1:], path)
				if request.Method == http.MethodGet && f.lookupMode != "" {
					code, status := "AuthorizationFailed", http.StatusForbidden
					if f.lookupMode == "unexpected404" {
						code, status = "EndpointNotFound", http.StatusNotFound
					}
					return reply(status, map[string]any{"error": map[string]any{"code": code}})
				}
				if !f.softDeleted[id] {
					return missing()
				}
				if request.Method == http.MethodGet {
					return reply(http.StatusOK, map[string]any{"id": path, "location": location})
				}
				require.Equal(t, http.MethodDelete, request.Method)
				f.softDeleted[id] = false
				f.purged = append(f.purged, id)
				return reply(http.StatusNoContent, nil)
			default:
				t.Fatalf("unexpected ARM request: %s", event)
				return nil, fmt.Errorf("unexpected ARM request: %s", event)
			}
		}),
	}}
	return p
}

func TestDestroyBYOAKSPurgeRecoversAcrossExecutions(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "false")
	t.Setenv(synthesis.BYOAKSEnvVar, "false")
	for _, failAt := range []string{
		"DELETE " + hostingDestroyAccountB,
		"DELETE " + hostingDestroyGroupID,
		"DELETE /subscriptions/sub/providers/Microsoft.CognitiveServices/locations/westus2/" +
			"resourceGroups/foundry/deletedAccounts/custom-account-b",
		"DELETE /subscriptions/sub/providers/Microsoft.Resources/deployments/azd-foundry-test-811c9dc5",
	} {
		t.Run(failAt, func(t *testing.T) {
			f := newHostingDestroyRecoveryFixture()
			f.failAt = failAt
			first := f.provider(t)
			result, err := first.Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.ErrorContains(t, err, "cleanup denied")
			require.Nil(t, result)
			require.False(t, f.live[hostingDestroyAccountA], "the first account was already deleted")
			retry := f.provider(t)
			require.NotSame(t, first, retry)
			f.failAt = ""
			result, err = retry.Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB}, f.purged,
				"retry must purge both already-deleted and remaining accounts")
			assert.NotContains(t, f.env.config, hostingDestroyPurgeKey)
		})
	}
}

func TestDestroyBYOAKSPurgePreservesAndMergesAccountScope(t *testing.T) {
	for _, ordinaryAtStart := range []bool{false, true} {
		t.Run(fmt.Sprint(ordinaryAtStart), func(t *testing.T) {
			f := newHostingDestroyRecoveryFixture()
			f.live[hostingDestroyAccountC] = ordinaryAtStart
			f.failAt = "DELETE " + hostingDestroyAccountB
			f.onRequest = func(event string) {
				if event == "DELETE "+hostingDestroyProjectA {
					var saved struct {
						EnvironmentName string `json:"environmentName"`
						ResourceGroupID string `json:"resourceGroupId"`
						Accounts        []struct {
							ResourceID string `json:"resourceId"`
							Name       string `json:"name"`
							Location   string `json:"location"`
						} `json:"accounts"`
					}
					require.NoError(t, json.Unmarshal(f.env.config[hostingDestroyPurgeKey], &saved),
						"the complete purge scope must be durable before the first Project deletion")
					require.Equal(t, "test", saved.EnvironmentName)
					require.Equal(t, hostingDestroyGroupID, saved.ResourceGroupID)
					if ordinaryAtStart {
						require.Len(t, saved.Accounts, 3, "ordinary accounts remain in the original group purge scope")
						require.Equal(t, hostingDestroyAccountC, saved.Accounts[2].ResourceID)
					} else {
						require.Len(t, saved.Accounts, 2)
					}
					require.Equal(t, hostingDestroyAccountA, saved.Accounts[0].ResourceID)
					require.Equal(t, "custom-account-a", saved.Accounts[0].Name)
					require.Equal(t, "eastus2", saved.Accounts[0].Location)
				}
			}
			result, err := f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			require.Error(t, err)
			require.Nil(t, result)
			var record accountPurgeRecord
			require.NoError(t, json.Unmarshal(f.env.config[hostingDestroyPurgeKey], &record))
			duplicate := record.Accounts[0]
			duplicate.ResourceID, duplicate.Location = strings.ToUpper(duplicate.ResourceID), strings.ToUpper(duplicate.Location)
			record.Accounts = append(record.Accounts, duplicate)
			f.env.config[hostingDestroyPurgeKey], err = json.Marshal(record)
			require.NoError(t, err)
			f.live[hostingDestroyAccountC] = true
			f.failAt, f.onRequest = "", nil
			result, err = f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB, hostingDestroyAccountC}, f.purged)
			assert.Empty(t, f.env.config)
		})
	}
}

func TestDestroyBYOAKSNoPurgeRetainsTargetForLaterPurge(t *testing.T) {
	for _, layer := range []bool{false, true} {
		t.Run(fmt.Sprint(layer), func(t *testing.T) {
			f := newHostingDestroyRecoveryFixture()
			f.layer = layer
			if layer {
				f.env.get[envKeyFoundryRG], f.env.get[envKeyFoundryRGOwner] = "foundry", hostingDestroyGroupID
				f.env.get[envKeyResourceGroup] = "sibling-group"
			}
			f.failAt = "DELETE " + hostingDestroyAccountB
			result, err := f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			require.Error(t, err)
			require.Nil(t, result)
			f.failAt = ""
			result, err = f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotEmpty(t, f.env.config[hostingDestroyPurgeKey])
			assert.Empty(t, f.purged)
			assert.NotContains(t, result.InvalidatedEnvKeys, envKeyFoundryRG)
			assert.NotContains(t, result.InvalidatedEnvKeys, envKeyFoundryRGOwner)
			for _, key := range result.InvalidatedEnvKeys {
				delete(f.env.get, key)
			}
			for _, event := range f.events {
				assert.NotContains(t, event, "/deletedAccounts/", "without --purge no permanent cleanup or reads are authorized")
			}
			f.events = nil
			result, err = f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB}, f.purged)
			assert.NotContains(t, f.env.config, hostingDestroyPurgeKey)
			assert.Contains(t, result.InvalidatedEnvKeys, envKeyFoundryRGOwner)
			assert.NotContains(t, f.events, "GET "+hostingDestroyAccountA, "the deleted Account need not be identified again")
			assert.NotContains(t, f.events, "DELETE /subscriptions/sub/resourceGroups/sibling-group")
		})
	}
}

func TestDestroyBYOAKSNoPurgeRetryPreservesFailedPurge(t *testing.T) {
	f := newHostingDestroyRecoveryFixture()
	f.failAt = "DELETE /subscriptions/sub/providers/Microsoft.CognitiveServices/locations/westus2/" +
		"resourceGroups/foundry/deletedAccounts/custom-account-b"
	result, err := f.provider(t).Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, []string{hostingDestroyAccountA}, f.purged)
	f.failAt, f.events = "", nil
	result, err = f.provider(t).Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{hostingDestroyAccountA}, f.purged)
	require.NotEmpty(t, f.env.config[hostingDestroyPurgeKey])
	for _, event := range f.events {
		assert.NotContains(t, event, "/deletedAccounts/")
	}
	result, err = f.provider(t).Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB}, f.purged)
	assert.Empty(t, f.env.config)
}

func TestDestroyBYOAKSPurgeRecoveryRetainsCancellationAndTimeout(t *testing.T) {
	for _, mode := range []string{"canceled", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			f := newHostingDestroyRecoveryFixture()
			f.failAt, f.failureMode = "DELETE "+hostingDestroyAccountB, mode
			result, err := f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.Error(t, err)
			require.Nil(t, result)
			require.False(t, f.live[hostingDestroyAccountA])
			require.NotEmpty(t, f.env.config[hostingDestroyPurgeKey])
			assert.NotContains(t, f.events, "DELETE "+hostingDestroyGroupID)
			f.failAt = ""
			result, err = f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB}, f.purged)
		})
	}
}

func TestDestroyBYOAKSPurgeSaveFailurePreventsAccountDeletion(t *testing.T) {
	f := newHostingDestroyRecoveryFixture()
	f.onRequest = func(event string) {
		if event == "GET "+hostingDestroyAccountA {
			f.env.configErr = errors.New("cannot save purge scope")
		}
	}
	result, err := f.provider(t).Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.ErrorContains(t, err, "cannot save purge scope")
	require.Nil(t, result)
	for _, event := range f.events {
		assert.False(t, strings.HasPrefix(event, "DELETE "), "save failure must precede Project, Account and group deletion")
	}
	assert.True(t, f.live[hostingDestroyAccountA])
	assert.Empty(t, f.env.config)
	f.env.configErr, f.onRequest = nil, nil
	result, err = f.provider(t).Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB}, f.purged)
}

func TestDestroyBYOAKSPurgeClearFailureRemainsRecoverable(t *testing.T) {
	f := newHostingDestroyRecoveryFixture()
	f.onRequest = func(event string) {
		if strings.HasPrefix(event, "DELETE /subscriptions/sub/providers/Microsoft.Resources/deployments/") {
			f.env.configErr = errors.New("cannot clear purge scope")
		}
	}
	result, err := f.provider(t).Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
	require.ErrorContains(t, err, "cannot clear purge scope")
	require.Nil(t, result)
	require.True(t, f.groupGone)
	require.NotEmpty(t, f.env.config[hostingDestroyPurgeKey])
	f.env.configErr, f.onRequest = nil, nil
	result, err = f.provider(t).Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB}, f.purged)
	assert.Empty(t, f.env.config)
}

func TestDestroyBYOAKSPurge404RequiresDeletedAccountAbsence(t *testing.T) {
	for _, mode := range []string{"denied", "unexpected404", "record still present"} {
		t.Run(mode, func(t *testing.T) {
			f := newHostingDestroyRecoveryFixture()
			f.failAt = "DELETE /subscriptions/sub/providers/Microsoft.CognitiveServices/locations/westus2/" +
				"resourceGroups/foundry/deletedAccounts/custom-account-b"
			result, err := f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.Error(t, err)
			require.Nil(t, result)
			if mode == "record still present" {
				f.failureMode = "notfound"
			} else {
				f.failAt, f.lookupMode = "", mode
			}
			result, err = f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.Error(t, err, "a purge 404 alone cannot certify completed cleanup")
			require.Nil(t, result)
			assert.True(t, f.softDeleted[hostingDestroyAccountB])
			require.NotEmpty(t, f.env.config[hostingDestroyPurgeKey])
			f.failAt, f.lookupMode = "", ""
			result, err = f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB}, f.purged)
		})
	}
}

func TestDestroyBYOAKSPurgeRecoveryRejectsOtherScopes(t *testing.T) {
	for _, change := range []string{"environment", "layer", "subscription", "group", "account", "location", "version"} {
		t.Run(change, func(t *testing.T) {
			f := newHostingDestroyRecoveryFixture()
			record := accountPurgeRecord{
				Version: 1, EnvironmentName: "test", ResourceGroupID: hostingDestroyGroupID,
				Accounts: []accountPurgeTarget{{ResourceID: hostingDestroyAccountA, Name: "custom-account-a", Location: "eastus2"}},
			}
			switch change {
			case "environment":
				record.EnvironmentName = "another-environment"
			case "layer":
				record.IsLayer, record.LayerName = true, "another-layer"
			case "subscription":
				record.ResourceGroupID = strings.Replace(record.ResourceGroupID, "/sub/", "/other-sub/", 1)
			case "group":
				record.ResourceGroupID = strings.Replace(record.ResourceGroupID, "/foundry", "/other-group", 1)
			case "account":
				record.Accounts[0].ResourceID = strings.Replace(record.Accounts[0].ResourceID, "/foundry/", "/other-group/", 1)
			case "location":
				record.Accounts[0].Location = "eastus2/other"
			case "version":
				record.Version = 2
			}
			data, err := json.Marshal(record)
			require.NoError(t, err)
			f.env.config = map[string][]byte{hostingDestroyPurgeKey: data}
			result, err := f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.ErrorContains(t, err, "account purge recovery state")
			require.Nil(t, result)
			assert.Empty(t, f.events, "invalid scope must be rejected before Azure operations")
			assert.Equal(t, data, f.env.config[hostingDestroyPurgeKey])
			assert.Zero(t, f.env.configSetN)
			assert.Zero(t, f.env.configUnsetN)
		})
	}
}

func TestDestroyBYOAKSPurgeOrdinaryFoundryKeepsExistingPath(t *testing.T) {
	for _, purge404 := range []bool{false, true} {
		t.Run(fmt.Sprint(purge404), func(t *testing.T) {
			f := newHostingDestroyRecoveryFixture()
			f.ordinary = true
			if purge404 {
				f.failAt = "DELETE /subscriptions/sub/providers/Microsoft.CognitiveServices/locations/eastus2/" +
					"resourceGroups/foundry/deletedAccounts/custom-account-a"
				f.failureMode = "notfound"
			}
			result, err := f.provider(t).Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			if purge404 {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB}, f.purged)
			}
			assert.Zero(t, f.env.configSetN, "ordinary Foundry without recovery state requires no new state writes")
			assert.Zero(t, f.env.configUnsetN)
			assert.NotContains(t, f.events, "DELETE "+hostingDestroyProjectA)
			assert.NotContains(t, f.events, "DELETE "+hostingDestroyAccountA)
			for _, event := range f.events {
				assert.False(t, strings.HasPrefix(event, "GET ") && strings.Contains(event, "/deletedAccounts/"))
			}
		})
	}
}

func TestDestroyBYOAKSPurgeRetainsRecoveredCreateACRWithoutPreview(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "false")
	f := newHostingDestroyRecoveryFixture()
	registryID := strings.Replace(testACRID, "/shared/", "/foundry/", 1)
	f.env.get["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = registryID
	f.env.get["AZD_FOUNDRY_ACR_MODE"] = "create"
	f.env.get["AZURE_AI_PROJECT_ID"] = hostingDestroyProjectA
	f.env.get["AZURE_CONTAINER_REGISTRY_ENDPOINT"] = "registry.azurecr.io"
	f.failAt = "DELETE " + hostingDestroyAccountB
	first := f.provider(t)
	deploymentID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + first.deploymentName()
	identityReads := 0
	request := func(_ context.Context, method, path, _ string, out any) error {
		switch {
		case method == http.MethodGet && path == deploymentID:
			return acrTestReply(out, map[string]any{"properties": map[string]any{
				"provisioningState": "Succeeded", "outputs": map[string]any{
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": registryID},
					"AZURE_AI_PROJECT_ID":                  map[string]any{"value": hostingDestroyProjectA},
				},
			}})
		case method == http.MethodGet && path == deploymentID+"/operations":
			return acrTestReply(out, map[string]any{"value": []any{map[string]any{
				"properties": map[string]any{
					"provisioningOperation": "Create", "provisioningState": "Succeeded",
					"targetResource": map[string]any{"id": registryID, "resourceType": "Microsoft.ContainerRegistry/registries"},
				},
			}}})
		case method == http.MethodGet && path == hostingDestroyProjectA:
			require.False(t, f.projectGone, "a new execution must not depend on the deleted Project identity")
			identityReads++
			return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "project-principal"}})
		case method == http.MethodDelete && path == deploymentID:
			require.True(t, f.groupGone)
			return nil
		default:
			t.Fatalf("unexpected ACR recovery request: %s %s", method, path)
			return &azcore.ResponseError{StatusCode: http.StatusNotFound}
		}
	}
	first.acrRequest = request
	result, err := first.Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
	require.ErrorContains(t, err, "cleanup denied")
	require.Nil(t, result)
	require.False(t, f.live[hostingDestroyAccountA])
	require.NotEmpty(t, f.env.config[synthesis.ACRBindingConfigKey])
	require.NotEmpty(t, f.env.config[hostingDestroyPurgeKey])
	f.failAt = ""
	retry := f.provider(t)
	retry.acrRequest = request
	result, err = retry.Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, identityReads)
	assert.Equal(t, []string{hostingDestroyAccountA, hostingDestroyAccountB}, f.purged)
	assert.Empty(t, f.env.config)
}

func newBYOAKSDestroyTestProvider(
	t *testing.T, failAt, failureMode string, deleteStatus int,
) (*FoundryProvisioningProvider, *[]string) {
	t.Helper()
	p, _ := acrTestProvider(t)
	p.rgExplicit = true
	p.credential = acrTestCredential{}
	events := []string{}
	completed := map[string]bool{}
	operations := map[string]string{}
	deploymentID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
	accountsPath := hostingDestroyGroupID + "/providers/Microsoft.CognitiveServices/accounts"
	account := func(id, name, location string) map[string]any {
		identities := map[string]any{hostingDestroyIdentity: map[string]any{"principalId": "hosting-principal"}}
		if id == hostingDestroyAccountB {
			identities[hostingDestroyExternalIdentity] = map[string]any{"principalId": "external-principal"}
		}
		return map[string]any{
			"id": id, "name": name, "location": location, "kind": "AIServices",
			"identity": map[string]any{
				"type": "SystemAssigned, UserAssigned", "userAssignedIdentities": identities,
			},
		}
	}
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Retry: policy.RetryOptions{MaxRetries: -1},
		Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
			path := request.URL.Path
			if strings.EqualFold(path, hostingDestroyGroupID) {
				path = hostingDestroyGroupID
			}
			event := request.Method + " " + path
			if request.Method == http.MethodDelete || operations[path] != "" {
				events = append(events, event)
			}
			reply := func(code int, value any) (*http.Response, error) {
				response, err := acrTestHTTPReply(t, request, code, value)
				if value == nil {
					response.Body = io.NopCloser(strings.NewReader(""))
				}
				return response, err
			}
			if event == failAt {
				switch failureMode {
				case "canceled":
					return nil, context.Canceled
				case "deadline":
					return nil, context.DeadlineExceeded
				case "missing-polling-url":
					return reply(http.StatusAccepted, nil)
				case "failed":
					return reply(http.StatusOK, map[string]any{
						"status": "Failed",
						"error":  map[string]any{"code": "CleanupFailed", "message": "Foundry cleanup failed"},
					})
				case "missing-properties":
					return reply(http.StatusOK, map[string]any{"id": path, "properties": nil})
				case "invalid-hosting":
					return reply(http.StatusOK, map[string]any{
						"properties": map[string]any{"agentHostingConfigurations": []any{map[string]any{
							"hostingType": "ManagedCluster", "hostingManagementIdentityResourceId": "/not-an-identity",
						}}},
					})
				case "wrong-project":
					return reply(http.StatusOK, map[string]any{"value": []any{map[string]any{
						"id": strings.Replace(hostingDestroyProjectA, "/foundry/", "/external/", 1),
					}}})
				default:
					return reply(http.StatusForbidden, map[string]any{
						"error": map[string]any{"code": "AuthorizationFailed", "message": "Foundry cleanup denied"},
					})
				}
			}
			if resourceID := operations[path]; resourceID != "" {
				require.Equal(t, http.MethodGet, request.Method)
				completed[resourceID] = true
				return reply(http.StatusOK, nil)
			}
			if request.Method == http.MethodGet {
				switch path {
				case deploymentID:
					return reply(http.StatusNotFound, map[string]any{"error": map[string]any{"code": "NotFound"}})
				case accountsPath:
					if request.URL.Query().Get("$skiptoken") != "" {
						return reply(http.StatusOK, map[string]any{
							"value": []any{account(hostingDestroyAccountB, "custom-account-b", "westus2")},
						})
					}
					return reply(http.StatusOK, map[string]any{
						"value": []any{account(hostingDestroyAccountA, "custom-account-a", "eastus2")},
						"nextLink": "https://management.azure.com" + accountsPath +
							"?api-version=2025-09-01&$skiptoken=accounts-2",
					})
				case hostingDestroyAccountA + "/deployments", hostingDestroyAccountB + "/deployments":
					return reply(http.StatusOK, map[string]any{"value": []any{map[string]any{"name": "model"}}})
				case hostingDestroyAccountA, hostingDestroyAccountB:
					require.Equal(t, "2026-07-15-preview", request.URL.Query().Get("api-version"))
					config := func(name, identity string) map[string]any {
						return map[string]any{
							"hostingType": "ManagedCluster", "name": name,
							"clusterResourceId": "/subscriptions/sub/resourceGroups/external/" +
								"providers/Microsoft.ContainerService/managedClusters/cluster",
							"hostingManagementIdentityResourceId": identity,
							"storageAccountResourceId": hostingDestroyGroupID +
								"/providers/Microsoft.Storage/storageAccounts/storage",
							"workloadIdentityResourceId": hostingDestroyGroupID +
								"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/workload",
						}
					}
					configs := []any{config("default", hostingDestroyIdentity)}
					if path == hostingDestroyAccountB {
						configs = append([]any{config("external", hostingDestroyExternalIdentity)}, configs...)
					}
					return reply(http.StatusOK, map[string]any{
						"id": path, "properties": map[string]any{"agentHostingConfigurations": configs},
					})
				case hostingDestroyAccountA + "/projects":
					if request.URL.Query().Get("$skiptoken") != "" {
						return reply(http.StatusOK, map[string]any{"value": []any{map[string]any{
							"id": hostingDestroyProjectB, "name": "custom-project-b",
						}}})
					}
					return reply(http.StatusOK, map[string]any{
						"value": []any{map[string]any{"id": hostingDestroyProjectA, "name": "custom-project-a"}},
						"nextLink": "https://management.azure.com" + path +
							"?api-version=2025-09-01&$skiptoken=projects-2",
					})
				case hostingDestroyAccountB + "/projects":
					return reply(http.StatusOK, map[string]any{"value": []any{}})
				}
			}
			if request.Method == http.MethodDelete {
				switch path {
				case hostingDestroyAccountA + "/deployments/model", hostingDestroyAccountB + "/deployments/model":
					completed[path] = true
					return reply(http.StatusOK, nil)
				case hostingDestroyProjectA, hostingDestroyProjectB, hostingDestroyAccountA, hostingDestroyAccountB:
					require.True(t, completed[hostingDestroyAccountA+"/deployments/model"])
					require.True(t, completed[hostingDestroyAccountB+"/deployments/model"])
					require.Equal(t, "2026-07-15-preview", request.URL.Query().Get("api-version"))
					if path == hostingDestroyAccountA {
						require.True(t, completed[hostingDestroyProjectA],
							"project deletion must finish before account deletion")
						require.True(t, completed[hostingDestroyProjectB], "all project pages must be cleaned up")
					}
					if deleteStatus != http.StatusAccepted {
						completed[path] = true
						return reply(deleteStatus, nil)
					}
					operationPath := "/operations/" + path[strings.LastIndex(path, "/")+1:]
					operations[operationPath] = path
					response, err := reply(http.StatusAccepted, nil)
					response.Header.Set("Location", "https://management.azure.com"+operationPath)
					if failureMode == "failed" && failAt == "GET "+operationPath {
						// JSON status documents use Azure-AsyncOperation; Location uses HTTP completion codes.
						response.Header.Set("Azure-AsyncOperation", "https://management.azure.com"+operationPath)
					}
					return response, err
				case hostingDestroyGroupID:
					require.True(t, completed[hostingDestroyAccountA], "Foundry must be gone before its hosting identity")
					require.True(t, completed[hostingDestroyAccountB],
						"every affected account must finish before group deletion")
					completed[path] = true
					return reply(http.StatusOK, nil)
				case "/subscriptions/sub/providers/Microsoft.CognitiveServices/locations/eastus2/" +
					"resourceGroups/foundry/deletedAccounts/custom-account-a",
					"/subscriptions/sub/providers/Microsoft.CognitiveServices/locations/westus2/" +
						"resourceGroups/foundry/deletedAccounts/custom-account-b":
					require.True(t, completed[hostingDestroyGroupID], "purge must follow resource-group deletion")
					return reply(http.StatusOK, nil)
				case deploymentID:
					require.True(t, completed[hostingDestroyGroupID], "deployment evidence must survive failed cleanup")
					return reply(http.StatusNoContent, nil)
				}
			}
			t.Fatalf("unexpected ARM request: %s", event)
			return nil, fmt.Errorf("unexpected ARM request: %s", event)
		}),
	}}
	return p, &events
}

func TestDestroyBYOAKSWaitsForFoundryBeforeResourceGroup(t *testing.T) {
	t.Setenv(synthesis.BYOAKSEnvVar, "false")
	p, events := newBYOAKSDestroyTestProvider(t, "", "", http.StatusAccepted)
	result, err := p.Destroy(t.Context(),
		&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.InvalidatedEnvKeys, "AZURE_AI_PROJECT_ID")
	assert.Contains(t, result.InvalidatedEnvKeys, "AZURE_AI_ACCOUNT_NAME")
	assert.Equal(t, []string{
		"DELETE " + hostingDestroyAccountA + "/deployments/model",
		"DELETE " + hostingDestroyAccountB + "/deployments/model",
		"DELETE " + hostingDestroyProjectA,
		"GET /operations/custom-project-a",
		"DELETE " + hostingDestroyProjectB,
		"GET /operations/custom-project-b",
		"DELETE " + hostingDestroyAccountA,
		"GET /operations/custom-account-a",
		"DELETE " + hostingDestroyAccountB,
		"GET /operations/custom-account-b",
		"DELETE " + hostingDestroyGroupID,
		"DELETE /subscriptions/sub/providers/Microsoft.CognitiveServices/locations/eastus2/" +
			"resourceGroups/foundry/deletedAccounts/custom-account-a",
		"DELETE /subscriptions/sub/providers/Microsoft.CognitiveServices/locations/westus2/" +
			"resourceGroups/foundry/deletedAccounts/custom-account-b",
		"DELETE /subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName(),
	}, *events)
}

func TestDestroyBYOAKSHandlesCompletedAndMissingResources(t *testing.T) {
	for _, statusCode := range []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			p, events := newBYOAKSDestroyTestProvider(t, "", "", statusCode)
			result, err := p.Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, []string{
				"DELETE " + hostingDestroyAccountA + "/deployments/model",
				"DELETE " + hostingDestroyAccountB + "/deployments/model",
				"DELETE " + hostingDestroyProjectA,
				"DELETE " + hostingDestroyProjectB,
				"DELETE " + hostingDestroyAccountA,
				"DELETE " + hostingDestroyAccountB,
				"DELETE " + hostingDestroyGroupID,
				"DELETE /subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName(),
			}, *events)
		})
	}
}

func TestDestroyBYOAKSFailureRetainsResourceGroup(t *testing.T) {
	for _, tt := range []struct {
		name, failAt, failureMode, operation, wantError string
	}{
		{name: "hosting read denied", failAt: "GET " + hostingDestroyAccountA,
			operation: exterrors.OpCognitiveAccountGet, wantError: "Foundry cleanup denied"},
		{name: "missing account properties", failAt: "GET " + hostingDestroyAccountA, failureMode: "missing-properties",
			operation: exterrors.OpCognitiveAccountGet, wantError: "account properties are missing"},
		{name: "invalid hosting metadata", failAt: "GET " + hostingDestroyAccountA, failureMode: "invalid-hosting",
			operation: exterrors.OpCognitiveAccountGet, wantError: "valid hosting-management identity"},
		{name: "projects list denied", failAt: "GET " + hostingDestroyAccountA + "/projects",
			operation: exterrors.OpCognitiveProjectList, wantError: "Foundry cleanup denied"},
		{name: "project outside account", failAt: "GET " + hostingDestroyAccountA + "/projects",
			failureMode: "wrong-project", operation: exterrors.CodeInvalidServiceConfig, wantError: "does not belong"},
		{name: "project delete denied", failAt: "DELETE " + hostingDestroyProjectA,
			operation: exterrors.OpCognitiveProjectDelete, wantError: "Foundry cleanup denied"},
		{name: "project asynchronous failure", failAt: "GET /operations/custom-project-a", failureMode: "failed",
			operation: exterrors.OpCognitiveProjectDelete, wantError: "Foundry cleanup failed"},
		{name: "account delete denied", failAt: "DELETE " + hostingDestroyAccountA,
			operation: exterrors.OpCognitiveAccountDelete, wantError: "Foundry cleanup denied"},
		{name: "account accepted without polling URL", failAt: "DELETE " + hostingDestroyAccountA,
			failureMode: "missing-polling-url", operation: exterrors.OpCognitiveAccountDelete, wantError: "missing polling URL"},
		{name: "last account asynchronous failure", failAt: "GET /operations/custom-account-b", failureMode: "failed",
			operation: exterrors.OpCognitiveAccountDelete, wantError: "Foundry cleanup failed"},
		{name: "project canceled", failAt: "GET /operations/custom-project-a", failureMode: "canceled",
			operation: exterrors.CodeCancelled, wantError: "cancelled"},
		{name: "account canceled", failAt: "GET /operations/custom-account-a", failureMode: "canceled",
			operation: exterrors.CodeCancelled, wantError: "cancelled"},
		{name: "project timed out", failAt: "GET /operations/custom-project-a", failureMode: "deadline",
			operation: exterrors.OpCognitiveProjectDelete, wantError: "deadline exceeded"},
		{name: "account timed out", failAt: "GET /operations/custom-account-a", failureMode: "deadline",
			operation: exterrors.OpCognitiveAccountDelete, wantError: "deadline exceeded"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, events := newBYOAKSDestroyTestProvider(t, tt.failAt, tt.failureMode, http.StatusAccepted)
			result, err := p.Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.ErrorContains(t, err, tt.wantError)
			require.Nil(t, result, "failed cleanup must not invalidate environment bindings")
			if service, ok := errors.AsType[*azdext.ServiceError](err); ok {
				assert.True(t, strings.HasPrefix(service.ErrorCode, tt.operation+"."))
			} else {
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "cleanup errors must retain their structured classification")
				assert.Equal(t, tt.operation, local.Code)
			}
			assert.NotContains(t, *events, "DELETE "+hostingDestroyGroupID)
			assert.NotContains(t, *events,
				"DELETE /subscriptions/sub/providers/Microsoft.Resources/deployments/"+p.deploymentName())
			for _, event := range *events {
				assert.NotContains(t, event, "/deletedAccounts/", "purge must not follow failed Foundry cleanup")
				assert.NotContains(t, event, "/resourceGroups/external/", "cleanup must not cross resource-group boundaries")
			}
		})
	}
}

func TestDestroyBYOAKSLeavesUnaffectedAccountsOnGroupPath(t *testing.T) {
	for _, tt := range []struct {
		name, kind, assignedIdentity, hostingType, hostingIdentity string
		wantHostingRead, existingProject                           bool
	}{
		{name: "ordinary Foundry", kind: "AIServices"},
		{name: "non-Foundry account", kind: "OpenAI", assignedIdentity: hostingDestroyIdentity},
		{name: "identity in another group", kind: "AIServices", assignedIdentity: hostingDestroyExternalIdentity},
		{name: "identity in another subscription with same group name", kind: "AIServices",
			assignedIdentity: strings.Replace(hostingDestroyIdentity, "/SUB/", "/other-sub/", 1)},
		{name: "same-group identity without AKS hosting", kind: "AIServices",
			assignedIdentity: hostingDestroyIdentity, wantHostingRead: true},
		{name: "hosting identity outside group with another same-group identity", kind: "AIServices",
			assignedIdentity: hostingDestroyIdentity, hostingType: "ManagedCluster",
			hostingIdentity: hostingDestroyExternalIdentity, wantHostingRead: true},
		{name: "hosting identity in another subscription", kind: "AIServices",
			assignedIdentity: hostingDestroyIdentity, hostingType: "ManagedCluster",
			hostingIdentity: strings.Replace(hostingDestroyIdentity, "/SUB/", "/other-sub/", 1), wantHostingRead: true},
		{name: "existing project with owned supporting group", kind: "AIServices", existingProject: true,
			assignedIdentity: hostingDestroyIdentity, hostingType: "ManagedCluster", hostingIdentity: hostingDestroyIdentity},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := acrTestProvider(t)
			p.rgExplicit = true
			p.credential = acrTestCredential{}
			existingAccountID := strings.Replace(hostingDestroyAccountB, "/foundry/", "/external/", 1)
			if tt.existingProject {
				p.brownfieldEndpoint = "https://existing.services.ai.azure.com/api/projects/project"
				p.existingProjectID = existingAccountID + "/projects/project"
				p.existingAcrMode = "none"
				p.foundryRGOwnerID = hostingDestroyGroupID
			}
			deploymentID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
			var deleted []string
			hostingRead := false
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					path := request.URL.Path
					if strings.EqualFold(path, hostingDestroyGroupID) {
						path = hostingDestroyGroupID
					}
					if request.Method == http.MethodDelete {
						deleted = append(deleted, path)
						require.Contains(t, []string{hostingDestroyGroupID, deploymentID}, path,
							"unaffected accounts must retain resource-group-only deletion")
						return acrTestHTTPReply(t, request, http.StatusOK, nil)
					}
					require.Equal(t, http.MethodGet, request.Method)
					switch path {
					case hostingDestroyGroupID:
						require.True(t, tt.existingProject)
						return acrTestHTTPReply(t, request, http.StatusOK,
							map[string]any{"tags": map[string]any{"azd-env-name": "test"}})
					case existingAccountID + "/projects/project":
						require.True(t, tt.existingProject)
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
							"properties": map[string]any{"endpoints": map[string]any{"AI Foundry API": p.brownfieldEndpoint}},
						})
					case existingAccountID:
						require.True(t, tt.existingProject)
						return acrTestHTTPReply(t, request, http.StatusOK,
							map[string]any{"properties": map[string]any{"customSubDomainName": "existing"}})
					case deploymentID:
						return acrTestHTTPReply(t, request, http.StatusNotFound, nil)
					case hostingDestroyGroupID + "/providers/Microsoft.CognitiveServices/accounts":
						value := map[string]any{
							"id": hostingDestroyAccountA, "name": "custom-account-a", "kind": tt.kind,
						}
						if tt.assignedIdentity != "" {
							identities := map[string]any{tt.assignedIdentity: map[string]any{}}
							if tt.hostingIdentity != "" {
								identities[tt.hostingIdentity] = map[string]any{}
							}
							value["identity"] = map[string]any{
								"userAssignedIdentities": identities,
							}
						}
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"value": []any{value}})
					case hostingDestroyAccountA + "/deployments":
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"value": []any{}})
					case hostingDestroyAccountA:
						require.True(t, tt.wantHostingRead, "this account must not require a preview hosting read")
						hostingRead = true
						var configs []any
						if tt.hostingType != "" {
							configs = []any{map[string]any{
								"hostingType": tt.hostingType, "hostingManagementIdentityResourceId": tt.hostingIdentity,
							}}
						}
						return acrTestHTTPReply(t, request, http.StatusOK,
							map[string]any{"properties": map[string]any{"agentHostingConfigurations": configs}})
					default:
						t.Fatalf("unexpected ARM request: %s %s", request.Method, path)
						return nil, fmt.Errorf("unexpected ARM request: %s", path)
					}
				}),
			}}
			result, err := p.Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.wantHostingRead, hostingRead)
			assert.Equal(t, []string{hostingDestroyGroupID, deploymentID}, deleted)
		})
	}
}
