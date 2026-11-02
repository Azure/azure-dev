// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestOwnershipRequiresMembershipEvenWithMatchingEnvironmentTag(t *testing.T) {
	resourceID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/app"
	for _, test := range []struct {
		name    string
		member  bool
		tag     string
		wantErr string
	}{
		{name: "untagged deployment member", member: true},
		{name: "tagged deployment member", member: true, tag: "test"},
		{name: "tag alone is not proof", tag: "test", wantErr: "not a member"},
		{name: "conflicting tag", member: true, tag: "other", wantErr: "another azd environment"},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := &deploymentBinding{resources: map[string]bool{
				strings.ToLower(resourceID): test.member,
			}}
			ctx := context.WithValue(t.Context(), deploymentBindingKey{}, binding)
			tags := map[string]*string{}
			if test.tag != "" {
				tags[azdEnvironmentTag] = new(test.tag)
			}
			err := checkResourceOwnership(ctx, nil, "test", nil, resourceID, tags)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOriginalOutputBindingsAreOperationLocal(t *testing.T) {
	values := map[string]string{
		"AZURE_RESOURCE_GROUP": "rg",
		"DATABASE_SERVER_ID": "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.DBforPostgreSQL/flexibleServers/pg",
		"DATABASE_HOST": "pg.postgres.database.azure.com",
		"APPLICATION_IDENTITY_ID": "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.ManagedIdentity/userAssignedIdentities/app",
		"APPLICATION_IDENTITY_CLIENT_ID": "11111111-1111-1111-1111-111111111111",
		"APPLICATION_PRINCIPAL_ID":       "22222222-2222-2222-2222-222222222222",
		"CONTAINER_APP_ENVIRONMENT_ID": "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.App/managedEnvironments/env",
		"AZURE_CONTAINER_REGISTRY_ENDPOINT": "registry.azurecr.io",
		databaseNameEnv:                     "petclinic",
	}
	outputs := deploymentOutputsFromValues(values)
	bound, err := boundDeploymentValues(values, outputs)
	require.NoError(t, err)
	require.Equal(t, "pg", bound[postgresServerNameEnv])
	require.NotContains(t, bound, postgresDatabaseEnv)
	require.Equal(t, values["APPLICATION_IDENTITY_ID"], bound[serviceIdentityIDEnv])
	require.NotContains(t, values, postgresHostEnv)

	outputs["DATABASE_HOST"] = currentDeploymentOutput{Type: "string", Value: "stale.postgres.database.azure.com"}
	_, err = boundDeploymentValues(values, outputs)
	require.ErrorContains(t, err, "DATABASE_HOST was not emitted consistently")
	outputs = deploymentOutputsFromValues(values)
	values[postgresHostEnv] = "foreign.postgres.database.azure.com"
	_, err = boundDeploymentValues(values, outputs)
	require.ErrorContains(t, err, "conflicts")
}

type bindingTestCredential struct{}

func (bindingTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type bindingTestTransport func(*http.Request) (*http.Response, error)

func (transport bindingTestTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func bindingTestResponse(t *testing.T, document any) *http.Response {
	t.Helper()
	data, err := json.Marshal(document)
	require.NoError(t, err)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}
}

func TestDeploymentMembershipTraversesNestedOperationsAndRejectsForeignScope(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "nested and paginated", true: "foreign scope"}[foreign], func(t *testing.T) {
			nested := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Resources/deployments/database"
			server := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg"
			if foreign {
				server = strings.Replace(server, "/rg/", "/foreign/", 1)
			}
			operation := func(resourceID string) map[string]any {
				return map[string]any{"properties": map[string]any{
					"provisioningOperation": "Create",
					"targetResource":        map[string]any{"id": resourceID},
				}}
			}
			requests := 0
			transport := bindingTestTransport(func(request *http.Request) (*http.Response, error) {
				requests++
				if strings.Contains(request.URL.Path, "/deployments/root/") {
					return bindingTestResponse(t, map[string]any{"value": []any{operation(nested)}}), nil
				}
				if request.URL.Query().Get("page") == "2" {
					return bindingTestResponse(t, map[string]any{
						"value": []any{operation(server + "/databases/petclinic")},
					}), nil
				}
				return bindingTestResponse(t, map[string]any{
					"value":    []any{operation(server)},
					"nextLink": "https://management.azure.com" + request.URL.Path + "?page=2",
				}), nil
			})
			client, err := armresources.NewDeploymentOperationsClient("sub", bindingTestCredential{}, &arm.ClientOptions{
				ClientOptions: azcore.ClientOptions{Transport: transport},
			})
			require.NoError(t, err)
			root, err := arm.ParseResourceID("/subscriptions/sub/providers/Microsoft.Resources/deployments/root")
			require.NoError(t, err)
			resources := map[string]bool{}
			err = collectDeploymentResourcesWithClient(t.Context(), client, root, "rg", resources)
			if foreign {
				require.ErrorContains(t, err, "outside the verified resource scope")
				return
			}
			require.NoError(t, err)
			require.Equal(t, 3, requests)
			require.True(t, resources[strings.ToLower(server)])
			require.True(t, resources[strings.ToLower(server+"/databases/petclinic")])
		})
	}
}

func TestBroadFirewallRecognitionDoesNotTreatAzureServicesRuleAsPublicIPv4(t *testing.T) {
	for _, test := range []struct {
		start, end string
		want       bool
	}{
		{start: "0.0.0.0", end: "255.255.255.255", want: true},
		{start: "0.0.0.0", end: "0.0.0.0"},
		{start: "1.0.0.0", end: "255.255.255.255"},
		{start: "203.0.113.1", end: "203.0.113.1"},
	} {
		properties := &armpostgresqlflexibleservers.FirewallRuleProperties{
			StartIPAddress: new(test.start), EndIPAddress: new(test.end),
		}
		require.Equal(t, test.want, isAllPublicIPv4Rule(properties))
	}
	require.False(t, isAllPublicIPv4Rule(nil))
}

func TestOriginalAdministratorInputsValidateBeforeAuthenticationOrWrites(t *testing.T) {
	for _, name := range []string{
		"DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
		databaseAdministratorNameEnv, databaseAdministratorObjectIDEnv,
		databasePasswordEnv, databaseAppPasswordEnv,
	} {
		clearProcessValue(t, name)
	}
	for _, test := range []struct {
		name   string
		inputs map[string]string
	}{
		{name: "invalid object ID", inputs: map[string]string{"DATABASE_ADMIN_OBJECT_ID": "not-an-object-id"}},
		{name: "explicit empty name", inputs: map[string]string{"DATABASE_ADMIN_NAME": ""}},
		{name: "conflicting names", inputs: map[string]string{
			"DATABASE_ADMIN_NAME": "other@example.com", databaseAdministratorNameEnv: "developer@example.com",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			project, _ := automaticPostgresProject(t)
			values := map[string]string{azureSubscriptionIDEnv: "sub"}
			maps.Copy(values, test.inputs)
			store := &fakeEnvironmentStore{name: "test", data: values}
			identity := &fakeDeveloperIdentityResolver{}
			runtime := &Runtime{environments: store, identities: identity}
			err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
			require.Error(t, err)
			require.Zero(t, identity.calls)
			require.Empty(t, store.writes)
		})
	}
}

func TestContainerAppEnvironmentUsesCurrentSDKFieldAndRejectsConflictingLegacyField(t *testing.T) {
	for _, test := range []struct {
		name            string
		current, legacy *string
		want            bool
	}{
		{name: "current field", current: new("env"), want: true},
		{name: "legacy field", legacy: new("env"), want: true},
		{name: "both agree", current: new("env"), legacy: new("env"), want: true},
		{name: "conflicting fields", current: new("env"), legacy: new("foreign")},
		{name: "foreign current field", current: new("foreign")},
		{name: "no fields"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, matchesContainerAppEnvironment(&armappcontainers.ContainerAppProperties{
				EnvironmentID: test.current, ManagedEnvironmentID: test.legacy,
			}, "env"))
		})
	}
	require.False(t, matchesContainerAppEnvironment(nil, "env"))
}
