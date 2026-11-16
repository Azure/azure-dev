// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/require"
)

func TestAzureBackendUsesConfiguredCloudEndpoints(t *testing.T) {
	for _, configured := range []cloud.Configuration{cloud.AzurePublic, cloud.AzureGovernment, cloud.AzureChina} {
		endpoint := strings.TrimSuffix(configured.Services[cloud.ResourceManager].Endpoint, "/")
		t.Run(endpoint, func(t *testing.T) {
			var requests []string
			transport := transportFunc(func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.URL.String())
				require.True(t, strings.HasPrefix(request.URL.String(), endpoint+"/"))
				body := `{"value":[]}`
				if strings.HasSuffix(request.URL.Path, "/resourcegroups/generated") {
					body = `{"name":"generated"}`
				} else if strings.HasSuffix(request.URL.Path, "/deployments/data") {
					body = `{"name":"data","properties":{"provisioningState":"Succeeded"}}`
				}
				return &http.Response{
					StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: request,
				}, nil
			})
			backend, err := newBackend("sub", testCredential{}, &arm.ClientOptions{
				ClientOptions: azcore.ClientOptions{Cloud: configured, Transport: transport},
			})
			require.NoError(t, err)
			_, err = backend.group(t.Context(), "generated")
			require.NoError(t, err)
			_, err = backend.get(t.Context(), "data")
			require.NoError(t, err)
			require.Len(t, requests, 3, "all three ARM clients must use the configured cloud")
		})
	}
}

type testCredential struct{}

func (testCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline-test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestAzureBackendMissingNestedDeployment(t *testing.T) {
	for _, statusCode := range []int{404, 403} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			transport := transportFunc(func(request *http.Request) (*http.Response, error) {
				body := `{"error":{"code":"NotFound","message":"gone"}}`
				code := statusCode
				if strings.HasSuffix(request.URL.Path, "/root/operations") {
					code = 200
					body = `{"value":[{"properties":{"targetResource":{"id":"` + groupID + `"}}},` +
						`{"properties":{"targetResource":{"id":"` + groupID +
						`/providers/Microsoft.Resources/deployments/nested"}}}]}`
				}
				return &http.Response{
					StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: request,
				}, nil
			})
			options := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
				Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
			}}
			operations, err := armresources.NewDeploymentOperationsClient("sub", testCredential{}, options)
			if err != nil {
				t.Fatal(err)
			}
			a := &azureBackend{operations: operations}
			resources := map[string]bool{}
			err = a.collectResources(t.Context(), "/subscriptions/sub", "root", resources, map[string]bool{})
			if statusCode == 404 {
				if err != nil || !resources[groupID] {
					t.Fatalf("missing nested deployment prevented retry: %v", err)
				}
			} else if err == nil {
				t.Fatal("authorization failure was ignored")
			}
		})
	}
}

func TestAzureBackendOfflineARMRequests(t *testing.T) {
	deploymentPath := "/subscriptions/sub/providers/Microsoft.Resources/deployments/layer"
	nestedPath := groupID + "/providers/Microsoft.Resources/deployments/resources"
	deploymentBody := `{"name":"layer","properties":{"provisioningState":"Succeeded",` +
		`"outputs":{"BOOL":{"type":"Bool","value":true}}}}`
	var requests []string
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		key := request.Method + " " + request.URL.Path
		requests = append(requests, key)
		code, body := 200, ""
		switch key {
		case "PUT " + deploymentPath, "GET " + deploymentPath:
			body = deploymentBody
		case "POST " + deploymentPath + "/whatIf":
			body = `{"status":"Succeeded","properties":{"changes":[{"resourceId":"` + groupID + `","changeType":"Create"}]}}`
		case "GET " + deploymentPath + "/operations":
			body = `{"value":[{"properties":{"targetResource":{"id":"` + groupID + `"}}},` +
				`{"properties":{"targetResource":{"id":"` + nestedPath + `"}}}]}`
		case "GET " + nestedPath + "/operations":
			body = `{"value":[{"properties":{"targetResource":{"id":"` + linkedID + `"}}}]}`
		case "GET " + strings.ToLower(groupID):
			body = `{"name":"generated","tags":{"azd-resources-owner":"layer"}}`
		case "DELETE " + strings.ToLower(groupID):
			code = 200
		case "DELETE " + deploymentPath:
			code = 204
		default:
			return nil, errors.New("unexpected ARM request: " + key)
		}
		return &http.Response{
			StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	options := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
	deployments, err := armresources.NewDeploymentsClient("sub", testCredential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := armresources.NewResourceGroupsClient("sub", testCredential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := armresources.NewDeploymentOperationsClient("sub", testCredential{}, options)
	if err != nil {
		t.Fatal(err)
	}
	azure := &azureBackend{
		subscription: "sub", deployments: deployments, groups: groups, operations: operations,
	}
	request := armresources.Deployment{Location: new("eastus"), Properties: &armresources.DeploymentProperties{
		Mode: new(armresources.DeploymentModeIncremental), Template: map[string]any{}, Parameters: map[string]any{},
	}}
	deployed, err := azure.deploy(t.Context(), "layer", request, nil)
	if err != nil || deployed.Properties == nil {
		t.Fatalf("deploy: %v", err)
	}
	changes, err := azure.preview(t.Context(), "layer", whatIfRequest(request), nil)
	if err != nil || len(changes) != 1 {
		t.Fatalf("what-if: changes %v, error %v", changes, err)
	}
	state, err := azure.get(t.Context(), "layer")
	if err != nil || len(state.Properties.OutputResources) != 3 {
		t.Fatalf("state resource recursion: %v, error %v", state.Properties, err)
	}
	group, err := azure.group(t.Context(), "generated")
	if err != nil || pointerValue(group.Tags[ownerTag]) != "layer" {
		t.Fatalf("get group: %v", err)
	}
	if err := azure.deleteGroup(t.Context(), "generated", nil); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if err := azure.deleteDeployment(t.Context(), "layer", nil); err != nil {
		t.Fatalf("delete deployment record: %v", err)
	}
	if len(requests) != 8 {
		t.Fatalf("unexpected ARM request sequence: %v", requests)
	}
}
