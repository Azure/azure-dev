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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUntaggedProjectRequiresBoundDeploymentAndOwnedParent(t *testing.T) {
	for _, scenario := range []string{
		"actual RP shape", "failed then succeeded attempts", "unrelated tags", "case insensitive IDs",
		"paged project Create", "partial project operations forbidden",
		"missing project Create", "project Read only", "failed project Create", "wrong project operation ID",
		"wrong project target", "missing parent operation ID", "parent Read only", "failed parent Create",
		"nested outside target RG", "operation forbidden", "wrong account ID", "account wrong owner",
		"account missing tags", "account wrong location", "account failed", "account forbidden",
		"project wrong ID", "project wrong location", "project wrong tenant", "project missing tenant",
		"project missing principal", "project wrong identity type", "project missing identity",
		"project missing properties", "project failed", "project missing state",
		"project conflicting tag", "project empty owner tag", "changed principal",
		"root content changed", "root ownership changed", "root parameters changed",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newCleanupARM(t, false, false)
			if scenario == "failed then succeeded attempts" {
				f.deploymentState = "Failed"
				require.Error(t, f.submit(t))
				f.deploymentState = ""
			}
			require.NoError(t, f.submit(t))
			project := f.base.resources[strings.ToLower(f.spec.ProjectID)]
			delete(project, "tags")
			account := f.base.resources[strings.ToLower(f.spec.AccountID)]
			identity := cleanupObject(t, project["identity"])
			switch scenario {
			case "unrelated tags":
				project["tags"] = map[string]any{"customer-tag": "value"}
			case "case insensitive IDs":
				project["id"] = strings.ToUpper(f.spec.ProjectID)
				account["id"] = strings.ToUpper(f.spec.AccountID)
			case "wrong account ID":
				account["id"] = f.spec.AccountID + "-other"
			case "account wrong owner":
				account["tags"] = ownerTags("another-owner")
			case "account missing tags":
				delete(account, "tags")
			case "account wrong location":
				account["location"] = "elsewhere"
			case "account failed":
				account["properties"] = map[string]any{"provisioningState": "Failed"}
			case "project wrong ID":
				project["id"] = f.spec.ProjectID + "-other"
			case "project wrong location":
				project["location"] = "elsewhere"
			case "project wrong tenant":
				identity["tenantId"] = "another-tenant"
			case "project missing tenant":
				delete(identity, "tenantId")
			case "project missing principal":
				delete(identity, "principalId")
			case "project wrong identity type":
				identity["type"] = "UserAssigned"
			case "project missing identity":
				delete(project, "identity")
			case "project missing properties":
				delete(project, "properties")
			case "project failed":
				project["properties"] = map[string]any{"provisioningState": "Failed"}
			case "project missing state":
				project["properties"] = map[string]any{}
			case "project conflicting tag":
				project["tags"] = ownerTags("another-owner")
			case "project empty owner tag":
				project["tags"] = ownerTags("")
			case "changed principal":
				state, err := f.base.journal.Load(t.Context())
				require.NoError(t, err)
				state.Foundry.ProjectPrincipalID = "previous-principal"
				require.NoError(t, f.base.journal.Save(t.Context(), state))
			}
			for _, root := range f.roots {
				properties := cleanupObject(t, root["properties"])
				switch scenario {
				case "root content changed":
					properties["template"] = map[string]any{"resources": []any{map[string]any{"name": "foreign"}}}
				case "root ownership changed":
					root["tags"] = ownerTags("another-owner")
				case "root parameters changed":
					cleanupObject(t, properties["parameters"])["foundryProjectName"] = map[string]any{"value": "foreign"}
				}
			}
			nestedID := "/subscriptions/" + f.base.plan.SubscriptionID + "/resourceGroups/" + f.base.plan.ResourceGroup +
				"/providers/Microsoft.Resources/deployments/foundry-resources"
			if scenario == "nested outside target RG" {
				nestedID = strings.Replace(nestedID, "/resourceGroups/"+f.base.plan.ResourceGroup+"/",
					"/resourceGroups/unrelated/", 1)
			}
			operation := func(parent, target, kind string) map[string]any {
				return map[string]any{
					"id": parent + "/operations/exact-operation",
					"properties": map[string]any{
						"provisioningOperation": "Create", "provisioningState": "Succeeded", "statusCode": "OK",
						"targetResource": map[string]any{"id": target, "resourceType": kind},
					},
				}
			}
			baseOptions := f.options
			var reads []string
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				reads = append(reads, request.Method+" "+request.URL.Path)
				if !strings.EqualFold(request.URL.Path, nestedID+"/operations") &&
					!strings.HasSuffix(strings.ToLower(request.URL.Path), "/operations") {
					if scenario == "account forbidden" && strings.EqualFold(request.URL.Path, f.spec.AccountID) {
						return contractARMResponse(request, 403, `{"error":{"code":"AccountEvidenceDenied"}}`), nil
					}
					return baseOptions.Transport.Do(request)
				}
				require.Equal(t, http.MethodGet, request.Method)
				if scenario == "operation forbidden" {
					return contractARMResponse(request, 403, `{"error":{"code":"GraphEvidenceDenied"}}`), nil
				}
				var operations []any
				nextLink := ""
				if strings.EqualFold(request.URL.Path, nestedID+"/operations") {
					if scenario == "partial project operations forbidden" && request.URL.Query().Get("$skiptoken") != "" {
						return contractARMResponse(request, 403, `{"error":{"code":"GraphEvidenceDenied"}}`), nil
					}
					create := operation(nestedID, f.spec.ProjectID, "Microsoft.CognitiveServices/accounts/projects")
					properties := cleanupObject(t, create["properties"])
					switch scenario {
					case "missing project Create":
						return contractARMResponse(request, 200, `{"value":[]}`), nil
					case "project Read only":
						properties["provisioningOperation"] = "Read"
					case "failed project Create":
						properties["provisioningState"] = "Failed"
					case "wrong project operation ID":
						create["id"] = nestedID + "-unrelated/operations/foreign"
					case "wrong project target":
						properties["targetResource"] = map[string]any{
							"id": f.spec.ProjectID + "-other", "resourceType": "Microsoft.CognitiveServices/accounts/projects",
						}
					}
					operations = []any{create}
					if (scenario == "paged project Create" || scenario == "partial project operations forbidden") &&
						request.URL.Query().Get("$skiptoken") == "" {
						nextLink = "https://management.azure.com" + request.URL.Path +
							"?api-version=2021-04-01&$skiptoken=next"
						if scenario == "paged project Create" {
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
					operations = page.Value
					rootID := strings.TrimSuffix(request.URL.Path, "/operations")
					link := operation(rootID, nestedID, "Microsoft.Resources/deployments")
					properties := cleanupObject(t, link["properties"])
					switch scenario {
					case "missing parent operation ID":
						delete(link, "id")
					case "parent Read only":
						properties["provisioningOperation"] = "Read"
					case "failed parent Create":
						properties["provisioningState"] = "Failed"
					}
					if scenario == "failed then succeeded attempts" && strings.HasSuffix(rootID, "owned-attempt-0") {
						properties["provisioningState"] = "Failed"
					}
					operations = append(operations, link)
				}
				raw, err := json.Marshal(map[string]any{"value": operations, "nextLink": nextLink})
				require.NoError(t, err)
				return contractARMResponse(request, 200, string(raw)), nil
			})
			lifecycle := NewLifecycle(contractCredential{}, options, f.base.journal)
			err := lifecycle.Observe(t.Context(), f.base.plan)
			switch scenario {
			case "actual RP shape", "failed then succeeded attempts", "unrelated tags", "case insensitive IDs",
				"paged project Create":
				require.NoError(t, err)
				state, err := f.base.journal.Load(t.Context())
				require.NoError(t, err)
				assert.Equal(t, identity["principalId"], state.Foundry.ProjectPrincipalID)
				assert.True(t, state.ResourceProgress["containerRegistry"].PreparationComplete)
				assert.Contains(t, reads, "GET "+f.spec.AccountID)
				report, err := lifecycle.Check(t.Context(), f.base.plan, f.spec)
				require.NoError(t, err)
				require.NoError(t, report.Err())
				f.options, f.lifecycle = options, lifecycle
				require.NoError(t, f.submit(t), "retry Start must recover the same untagged project")
				delete(f.base.resources[strings.ToLower(f.spec.ProjectID)], "tags")
				require.NoError(t, lifecycle.Observe(t.Context(), f.base.plan))
				require.NoError(t, lifecycle.Cleanup(t.Context(), f.base.plan, false, nil),
					"untagged exact project must use the same ownership proof before cascading deletion")
			default:
				require.Error(t, err)
				for _, request := range reads {
					assert.NotContains(t, request, "PUT ")
					assert.NotContains(t, request, "DELETE ")
				}
				state, loadErr := f.base.journal.Load(t.Context())
				require.NoError(t, loadErr)
				assert.False(t, state.ResourceProgress["containerRegistry"].PreparationComplete)
				require.Error(t, lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
				assert.Empty(t, f.deletes, "unverified project must retain account and dependencies")
				if strings.Contains(scenario, "forbidden") {
					response, ok := errors.AsType[*azcore.ResponseError](err)
					require.True(t, ok)
					assert.Equal(t, http.StatusForbidden, response.StatusCode)
				}
			}
		})
	}
}
