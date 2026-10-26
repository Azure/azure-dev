// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryConnectionCurrentIdentityDoesNotDiscloseResponseBody(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var messages strings.Builder
			log.SetListener(func(_ log.Event, message string) { messages.WriteString(message) })
			defer log.SetListener(nil)
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, request.Method)
				require.Equal(t, "/subscriptions/s/resourceGroups/g/providers/Microsoft.CognitiveServices/"+
					"accounts/a/projects/p/connections/r-conn/listSecrets", request.URL.Path)
				return contractARMResponse(request, status, `{"properties":{"authType":"ApiKey",`+
					`"target":"https://private-user:private-password@host/?sig=private-token#private-fragment",`+
					`"credentials":{"key":"private-key"}},"error":{"code":"AuthorizationFailed",`+
					`"message":"private-password private-key"}}`), nil
			})
			options.Logging.IncludeBody = true
			lifecycle := &ownedLifecycle{credential: contractCredential{}, options: options}
			state := &PreparationState{Foundry: &FoundryState{
				Spec: FoundrySpec{
					RegistryID: "/subscriptions/s/resourceGroups/g/providers/Microsoft.ContainerRegistry/registries/r",
				},
			}}
			err := lifecycle.verifyCurrentRegistryConnection(t.Context(), state,
				"/subscriptions/s/resourceGroups/g/providers/Microsoft.CognitiveServices/accounts/a/projects/p/connections/r-conn",
				"r.azurecr.io")
			require.Error(t, err)
			output := messages.String() + err.Error()
			for _, secret := range []string{
				"private-user", "private-password", "private-token", "private-fragment", "private-key",
			} {
				assert.NotContains(t, output, secret)
			}
			assert.True(t, options.Logging.IncludeBody, "the injected client's options must not be mutated")
			if status == http.StatusForbidden {
				response, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, "AuthorizationFailed", response.ErrorCode)
				assert.Equal(t, status, response.StatusCode)
				assert.Contains(t, err.Error(), "connections/listSecrets/action")
			}
		})
	}
}

func TestRegistryConnectionMaskedCredentialContract(t *testing.T) {
	for _, scenario := range []string{
		"null credentials", "omitted credentials", "explicit matching credentials", "case insensitive IDs",
		"tagless Project", "paged connection operations", "ABAC registry",
		"current identity drifts after completion",
		"wrong connection ID", "wrong category", "wrong auth type", "wrong target", "wrong registry",
		"workspace identity", "unknown workspace identity", "top level UAMI", "wrong explicit principal",
		"explicit UAMI resource", "empty explicit credentials", "partial explicit credentials",
		"credential bearing target", "missing connection", "connection forbidden",
		"missing connection Create", "connection Read", "connection failed", "wrong connection operation ID",
		"wrong connection target", "parent Read", "parent failed", "parent wrong operation ID",
		"operations forbidden", "partial operations forbidden", "wrong actual Project principal",
		"wrong Project tenant", "Project UAMI", "wrong owner account", "account forbidden",
		"wrong grant scope", "wrong grant principal", "wrong grant role", "conditional grant",
		"delegated grant", "wrong grant owner", "grant missing", "grant forbidden", "grant failed operation",
		"root template changed", "root parameters changed",
		"current connection forbidden", "current connection not found", "current connection unsupported API",
		"current connection empty", "current credentials null", "current wrong principal", "current UAMI resource",
		"current wrong target", "current wrong registry", "current wrong connection ID", "current workspace identity",
		"current auth changed", "current top level UAMI", "current connection malformed",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newCleanupARM(t, false, true)
			if scenario == "ABAC registry" {
				f.spec.RegistryABAC = true
				f.spec.Deployment.RegistryABAC = true
				cleanupObject(t, f.base.resources[strings.ToLower(f.base.plan.Registry.ID)]["properties"])["roleAssignmentMode"] =
					"AbacRepositoryPermissions"
			}

			require.NoError(t, f.submit(t))
			connectionID := f.spec.ProjectID + "/connections/" + f.base.plan.Registry.Name + "-conn"
			connection := f.base.resources[strings.ToLower(connectionID)]
			properties := cleanupObject(t, connection["properties"])
			explicit := properties["credentials"]
			properties["credentials"], properties["useWorkspaceManagedIdentity"] = nil, false
			switch scenario {
			case "omitted credentials":
				delete(properties, "credentials")
			case "explicit matching credentials":
				properties["credentials"] = explicit
			case "case insensitive IDs":
				connection["id"] = strings.ToUpper(connectionID)
				cleanupObject(t, properties["metadata"])["ResourceId"] = strings.ToUpper(f.spec.RegistryID)
			case "wrong connection ID":
				connection["id"] = connectionID + "-other"
			case "wrong category":
				properties["category"] = "CustomKeys"
			case "wrong auth type":
				properties["authType"] = "ApiKey"
			case "wrong target":
				properties["target"] = "another.azurecr.io"
			case "credential bearing target":
				properties["target"] = "https://private-user:private-password@registry.test/?sig=private-token#private-fragment"
			case "wrong registry":
				cleanupObject(t, properties["metadata"])["ResourceId"] = f.spec.RegistryID + "-other"
			case "workspace identity":
				properties["useWorkspaceManagedIdentity"] = true
			case "unknown workspace identity":
				delete(properties, "useWorkspaceManagedIdentity")
			case "top level UAMI":
				connection["identity"] = map[string]any{"type": "UserAssigned",
					"userAssignedIdentities": map[string]any{f.base.plan.Hosting.ID: map[string]any{}}}
			case "wrong explicit principal":
				properties["credentials"] = map[string]any{"clientId": "wrong-principal", "resourceId": f.spec.RegistryID}
			case "explicit UAMI resource":
				properties["credentials"] = map[string]any{
					"clientId": "00000000-0000-0000-0000-000000000004", "resourceId": f.base.plan.Hosting.ID,
				}
			case "empty explicit credentials":
				properties["credentials"] = map[string]any{}
			case "partial explicit credentials":
				properties["credentials"] = map[string]any{"resourceId": f.spec.RegistryID}
			case "missing connection":
				delete(f.base.resources, strings.ToLower(connectionID))
			case "tagless Project":
				delete(f.base.resources[strings.ToLower(f.spec.ProjectID)], "tags")
			}
			project := f.base.resources[strings.ToLower(f.spec.ProjectID)]
			identity := cleanupObject(t, project["identity"])
			switch scenario {
			case "wrong actual Project principal":
				identity["principalId"] = "wrong-principal"
			case "wrong Project tenant":
				identity["tenantId"] = "wrong-tenant"
			case "Project UAMI":
				identity["type"] = "UserAssigned"
			case "wrong owner account":
				f.base.resources[strings.ToLower(f.spec.AccountID)]["tags"] = ownerTags("foreign")
			}
			grantID := ""
			for id, resource := range f.base.resources {
				p, ok := resource["properties"].(map[string]any)
				if !ok || p["scope"] != f.spec.RegistryID ||
					p["principalId"] != "00000000-0000-0000-0000-000000000004" {
					continue
				}
				grantID = id
				switch scenario {
				case "wrong grant scope":
					p["scope"] = f.spec.RegistryID + "-other"
				case "wrong grant principal":
					p["principalId"] = "wrong-principal"
				case "wrong grant role":
					p["roleDefinitionId"] = "/subscriptions/foundry-sub/providers/Microsoft.Authorization/roleDefinitions/wrong"
				case "conditional grant":
					p["condition"] = "unsupported"
				case "delegated grant":
					p["delegatedManagedIdentityResourceId"] = f.base.plan.Workload.ID
				case "wrong grant owner":
					p["description"] = "foreign"
				case "grant missing":
					delete(f.base.resources, id)
				case "grant failed operation":
					f.operationStates = map[string]string{id: "Failed"}
				}
			}
			require.NotEmpty(t, grantID)
			for _, root := range f.roots {
				p := cleanupObject(t, root["properties"])
				switch scenario {
				case "root template changed":
					p["template"] = map[string]any{"resources": []any{map[string]any{"name": "foreign"}}}
				case "root parameters changed":
					cleanupObject(t, p["parameters"])["foundryProjectName"] = map[string]any{"value": "foreign"}
				}
			}
			baseOptions := f.options
			var reads []string
			currentDrift := false
			nestedID := "/subscriptions/" + f.base.plan.SubscriptionID + "/resourceGroups/" + f.base.plan.ResourceGroup +
				"/providers/Microsoft.Resources/deployments/registry-module"
			operation := func(parent, target, kind string) map[string]any {
				return map[string]any{"id": parent + "/operations/verified-create",
					"properties": map[string]any{
						"provisioningOperation": "Create", "provisioningState": "Succeeded", "statusCode": "OK",
						"targetResource": map[string]any{"id": target, "resourceType": kind},
					}}
			}
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				reads = append(reads, request.Method+" "+request.URL.Path)
				if strings.HasSuffix(request.URL.Path, "/listSecrets") {
					require.Equal(t, http.MethodPost, request.Method)
					require.Equal(t, connectionID+"/listSecrets", request.URL.Path)
					require.Equal(t, "2026-07-15-preview", request.URL.Query().Get("api-version"))
					require.True(t, request.Body == nil || request.Body == http.NoBody)
					if scenario == "current connection forbidden" || scenario == "current connection not found" ||
						scenario == "current connection unsupported API" {
						status := http.StatusForbidden
						if scenario == "current connection not found" {
							status = http.StatusNotFound
						} else if scenario == "current connection unsupported API" {
							status = http.StatusBadRequest
						}
						return contractARMResponse(request, status, `{"error":{"code":"CurrentIdentityUnavailable",`+
							`"message":"private-password private-token private-fragment"}}`), nil
					}
					if scenario == "current connection malformed" {
						return contractARMResponse(request, 200, `{"properties":`), nil
					}
					if scenario == "current connection empty" {
						return contractARMResponse(request, 200, `{}`), nil
					}
					currentProperties := map[string]any{
						"authType": "ManagedIdentity", "category": "ContainerRegistry",
						"credentials": explicit, "useWorkspaceManagedIdentity": false,
						"metadata": map[string]any{"ResourceId": f.spec.RegistryID},
						"target":   properties["target"],
					}
					current := map[string]any{"id": connectionID, "properties": currentProperties}
					if currentDrift {
						currentProperties["credentials"] = map[string]any{
							"clientId": "replacement-principal", "resourceId": f.spec.RegistryID,
						}
					}
					switch scenario {
					case "current credentials null":
						currentProperties["credentials"] = nil
					case "current wrong principal":
						currentProperties["credentials"] = map[string]any{
							"clientId": "other-principal", "resourceId": f.spec.RegistryID,
						}
					case "current UAMI resource":
						currentProperties["credentials"] = map[string]any{
							"clientId": "00000000-0000-0000-0000-000000000004", "resourceId": f.base.plan.Hosting.ID,
						}
					case "current wrong target":
						currentProperties["target"] = "https://private-user:private-password@registry.test/?sig=private-token"
					case "current wrong registry":
						currentProperties["metadata"] = map[string]any{"ResourceId": f.spec.RegistryID + "-foreign"}
					case "current wrong connection ID":
						current["id"] = connectionID + "-foreign"
					case "current workspace identity":
						currentProperties["useWorkspaceManagedIdentity"] = true
					case "current auth changed":
						currentProperties["authType"] = "ApiKey"
						currentProperties["credentials"] = map[string]any{"key": "private-token"}
					case "current top level UAMI":
						current["identity"] = map[string]any{"type": "UserAssigned"}
					}
					raw, err := json.Marshal(current)
					require.NoError(t, err)
					return contractARMResponse(request, 200, string(raw)), nil
				}
				if (scenario == "connection forbidden" && strings.EqualFold(request.URL.Path, connectionID)) ||
					(scenario == "account forbidden" && strings.EqualFold(request.URL.Path, f.spec.AccountID)) ||
					(scenario == "grant forbidden" && strings.EqualFold(request.URL.Path, grantID)) {
					return contractARMResponse(request, 403, `{"error":{"code":"ConnectionEvidenceDenied"}}`), nil
				}
				if !strings.HasSuffix(strings.ToLower(request.URL.Path), "/operations") {
					return baseOptions.Transport.Do(request)
				}
				require.Equal(t, http.MethodGet, request.Method)
				if scenario == "operations forbidden" || scenario == "partial operations forbidden" &&
					request.URL.Query().Get("$skiptoken") != "" {
					return contractARMResponse(request, 403, `{"error":{"code":"ConnectionGraphDenied"}}`), nil
				}
				var operations []any
				nextLink := ""
				if strings.EqualFold(request.URL.Path, nestedID+"/operations") {
					create := operation(nestedID, connectionID, "Microsoft.CognitiveServices/accounts/projects/connections")
					p := cleanupObject(t, create["properties"])
					switch scenario {
					case "connection Read":
						p["provisioningOperation"] = "Read"
					case "connection failed":
						p["provisioningState"] = "Failed"
					case "wrong connection operation ID":
						create["id"] = nestedID + "-foreign/operations/create"
					case "wrong connection target":
						cleanupObject(t, p["targetResource"])["id"] = connectionID + "-foreign"
					}
					if scenario != "missing connection Create" {
						operations = append(operations, create)
					}
					if (scenario == "paged connection operations" || scenario == "partial operations forbidden") &&
						request.URL.Query().Get("$skiptoken") == "" {
						nextLink = "https://management.azure.com" + request.URL.Path +
							"?api-version=2021-04-01&$skiptoken=next"
						if scenario == "paged connection operations" {
							operations = nil
						}
					}
				} else {
					response, err := baseOptions.Transport.Do(request)
					require.NoError(t, err)
					raw, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					var page struct {
						Value []any `json:"value"`
					}
					require.NoError(t, json.Unmarshal(raw, &page))
					for _, value := range page.Value {
						op := cleanupObject(t, value)
						p := cleanupObject(t, op["properties"])
						target := cleanupObject(t, p["targetResource"])
						if target["resourceType"] != "Microsoft.CognitiveServices/accounts/projects/connections" {
							operations = append(operations, value)
						}
					}
					rootID := strings.TrimSuffix(request.URL.Path, "/operations")
					link := operation(rootID, nestedID, "Microsoft.Resources/deployments")
					p := cleanupObject(t, link["properties"])
					switch scenario {
					case "parent Read":
						p["provisioningOperation"] = "Read"
					case "parent failed":
						p["provisioningState"] = "Failed"
					case "parent wrong operation ID":
						link["id"] = rootID + "-foreign/operations/create"
					}
					operations = append(operations, link,
						operation(rootID, f.spec.ProjectID, "Microsoft.CognitiveServices/accounts/projects"))
				}
				raw, err := json.Marshal(map[string]any{"value": operations, "nextLink": nextLink})
				require.NoError(t, err)
				return contractARMResponse(request, 200, string(raw)), nil
			})
			lifecycle := NewLifecycle(contractCredential{}, options, f.base.journal)
			err := lifecycle.Observe(t.Context(), f.base.plan)
			switch scenario {
			case "current identity drifts after completion":
				require.NoError(t, err)
				state, err := f.base.journal.Load(t.Context())
				require.NoError(t, err)
				require.True(t, state.ResourceProgress["containerRegistry"].PreparationComplete)
				beforeAttempts := len(state.Foundry.Attempts)
				currentDrift = true
				require.ErrorContains(t, lifecycle.Observe(t.Context(), f.base.plan), "current project registry connection")
				state, err = f.base.journal.Load(t.Context())
				require.NoError(t, err)
				assert.False(t, state.ResourceProgress["containerRegistry"].PreparationComplete)
				retryID := "/subscriptions/" + f.base.plan.SubscriptionID +
					"/providers/Microsoft.Resources/deployments/blocked-retry"
				require.Error(t, lifecycle.Start(t.Context(), f.base.plan, f.spec, retryID))
				state, err = f.base.journal.Load(t.Context())
				require.NoError(t, err)
				assert.Len(t, state.Foundry.Attempts, beforeAttempts)
			case "null credentials", "omitted credentials", "explicit matching credentials", "case insensitive IDs",
				"tagless Project", "paged connection operations", "ABAC registry":
				require.NoError(t, err)
				state, err := f.base.journal.Load(t.Context())
				require.NoError(t, err)
				assert.True(t, state.ResourceProgress["containerRegistry"].PreparationComplete)
				f.options, f.lifecycle = options, lifecycle
				require.NoError(t, f.submit(t), "retry Start must observe the original masked connection before another PUT")
				newProperties := cleanupObject(t, f.base.resources[strings.ToLower(connectionID)]["properties"])
				newProperties["credentials"], newProperties["useWorkspaceManagedIdentity"] = nil, false
				require.NoError(t, lifecycle.Observe(t.Context(), f.base.plan))
				require.NoError(t, lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			default:
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private-user")
				assert.NotContains(t, err.Error(), "private-password")
				assert.NotContains(t, err.Error(), "private-token")
				assert.NotContains(t, err.Error(), "private-fragment")
				state, loadErr := f.base.journal.Load(t.Context())
				require.NoError(t, loadErr)
				assert.False(t, state.ResourceProgress["containerRegistry"].PreparationComplete)
				if scenario != "missing connection" {
					beforeAttempts := len(state.Foundry.Attempts)
					retryID := "/subscriptions/" + f.base.plan.SubscriptionID +
						"/providers/Microsoft.Resources/deployments/blocked-retry"
					require.Error(t, lifecycle.Start(t.Context(), f.base.plan, f.spec, retryID),
						"retry must not admit another deployment on unverified evidence")
					state, loadErr = f.base.journal.Load(t.Context())
					require.NoError(t, loadErr)
					assert.Len(t, state.Foundry.Attempts, beforeAttempts)
				}
				for _, request := range reads {
					assert.NotContains(t, request, "PUT ")
					assert.NotContains(t, request, "DELETE ")
					if strings.HasPrefix(request, "POST ") {
						assert.True(t, strings.HasSuffix(request, "/listSecrets") || strings.HasSuffix(request, "/exportTemplate"),
							"only the exact read-only ARM actions are allowed")
					}
				}
				if strings.Contains(scenario, "forbidden") {
					response, ok := errors.AsType[*azcore.ResponseError](err)
					require.True(t, ok)
					assert.Equal(t, http.StatusForbidden, response.StatusCode)
				}
			}
		})
	}
}
