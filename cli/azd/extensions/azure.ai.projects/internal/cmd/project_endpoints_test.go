// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore Chdir

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type endpointTestCredential struct{}

func (endpointTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type endpointTestTransport func(*http.Request) (*http.Response, error)

func (f endpointTestTransport) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func resolvedCustomSubdomainProject(t *testing.T) *resolvedProject {
	t.Helper()
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/resource-account/projects/project"
	project, err := projectFromResourceID(projectID)
	require.NoError(t, err)
	var requests []string
	project, err = hydrateResolvedProject(t.Context(), project, endpointTestCredential{},
		&arm.ClientOptions{ClientOptions: policy.ClientOptions{
			Transport: endpointTestTransport(func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.URL.Path)
				body := `{"location":"eastus","properties":{"endpoints":{"AI Foundry API":` +
					`"https://custom-dns.services.ai.azure.com/api/projects/project"}}}`
				if request.URL.Path != projectID {
					require.Equal(t, strings.TrimSuffix(projectID, "/projects/project"), request.URL.Path)
					body = `{"properties":{"customSubDomainName":"custom-dns"}}`
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: request,
				}, nil
			}),
		}})
	require.NoError(t, err)
	require.Equal(t, []string{projectID, strings.TrimSuffix(projectID, "/projects/project")}, requests)
	require.Equal(t, projectID, project.ResourceId)
	require.Equal(t, "resource-account", project.AccountName)
	require.Equal(t, "eastus", project.Location)
	require.Equal(t, "https://custom-dns.services.ai.azure.com/api/projects/project", project.Endpoint)
	require.Equal(t, "https://custom-dns.openai.azure.com/", project.OpenAIEndpoint)
	return project
}

func TestHydrateResolvedProjectClassifiesLookupFailures(t *testing.T) {
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/resource-account/projects/project"
	baseProject, err := projectFromResourceID(projectID)
	require.NoError(t, err)

	t.Run("arm response error becomes service error", func(t *testing.T) {
		project := *baseProject
		_, err := hydrateResolvedProject(t.Context(), &project, endpointTestCredential{},
			&arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: endpointTestTransport(func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(
							`{"error":{"code":"AuthorizationFailed","message":"access denied"}}`)),
						Request: request,
					}, nil
				}),
			}})
		require.Error(t, err)
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok)
		require.Equal(t, http.StatusForbidden, serviceErr.StatusCode)
		require.Contains(t, serviceErr.Message, "AuthorizationFailed")
		require.Contains(t, serviceErr.ErrorCode, "cognitive_account_list")
	})

	t.Run("malformed metadata becomes validation error", func(t *testing.T) {
		project := *baseProject
		_, err := hydrateResolvedProject(t.Context(), &project, endpointTestCredential{},
			&arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: endpointTestTransport(func(request *http.Request) (*http.Response, error) {
					body := `{"properties":{"endpoints":{"AI Foundry API":"https://example.com/api/projects/project"}}}`
					if request.URL.Path != projectID {
						body = `{"properties":{"customSubDomainName":"custom-dns"}}`
					}
					return &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(body)), Request: request,
					}, nil
				}),
			}})
		require.Error(t, err)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	})
}

func TestProjectCustomSubdomainAuthoringAndEndpointRepair(t *testing.T) {
	project := resolvedCustomSubdomainProject(t)
	const legacyEndpoint = "https://resource-account.services.ai.azure.com/api/projects/project"
	old := map[string]string{
		"AZURE_AI_PROJECT_ID":                           project.ResourceId,
		"AZURE_AI_ACCOUNT_NAME":                         project.AccountName,
		"AZURE_AI_PROJECT_NAME":                         project.ProjectName,
		"FOUNDRY_PROJECT_ENDPOINT":                      legacyEndpoint,
		"AZURE_OPENAI_ENDPOINT":                         "https://resource-account.openai.azure.com/",
		"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT": legacyEndpoint,
	}
	for _, key := range projectReplacementEnvironmentKeys {
		old[key] = "preserved-" + key
	}
	environment := &transactionEnvironmentServer{values: maps.Clone(old)}
	envClient := newProjectEnvironmentClient(t, environment)
	section, err := structpb.NewStruct(map[string]any{
		"project": map[string]any{"endpoint": legacyEndpoint},
	})
	require.NoError(t, err)
	server := &transactionProjectServer{
		project: &azdext.ProjectConfig{
			Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
				"project": {Name: "project", Host: aiProjectHost},
			},
		},
		section: section,
	}
	client := newTransactionProjectClient(t, server)
	reconciler := &projectServiceReconciler{client: client}

	for attempt := range 2 {
		service := &projectServiceInfo{Resolved: map[string]any{
			"endpoint": environment.values["FOUNDRY_PROJECT_ENDPOINT"],
		}}
		require.NoError(t, confirmExplicitProjectReplacement(t.Context(), nil, project,
			service, environment.values, &projectAddFlags{projectID: project.ResourceId, noPrompt: true}))
		replaced := projectIdentityChanged(environment.values, serviceEndpoint(service.Resolved), project)
		require.False(t, replaced)
		_, action, _, err := reconciler.reconcileEndpoint(t.Context(), "project", project.Endpoint, project.Mode)
		require.NoError(t, err)
		if attempt == 0 {
			require.Equal(t, "updated", action)
			require.Len(t, server.serviceValues, 1)
			require.Equal(t, "endpoint", server.serviceValue.Path)
			require.Equal(t, project.Endpoint, server.serviceValue.Value.GetStringValue())
			server.section, err = structpb.NewStruct(map[string]any{
				"project": map[string]any{"endpoint": server.serviceValue.Value.GetStringValue()},
			})
			require.NoError(t, err)
		} else {
			require.Equal(t, "unchanged", action)
			require.Len(t, server.serviceValues, 1)
		}
		_, _, err = reconcileProjectEnvironmentWithRollback(t.Context(), envClient, "test",
			project.Mode, project, replaced)
		require.NoError(t, err)
		for _, key := range projectReplacementEnvironmentKeys {
			require.Equal(t, old[key], environment.values[key], key)
		}
		require.Equal(t, project.ResourceId, environment.values["AZURE_AI_PROJECT_ID"])
		require.Equal(t, project.Endpoint, environment.values["FOUNDRY_PROJECT_ENDPOINT"])
		require.Equal(t, project.Endpoint, environment.values["AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT"])
		require.Equal(t, project.OpenAIEndpoint, environment.values["AZURE_OPENAI_ENDPOINT"])
		authored, ok := server.section.AsMap()["project"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, project.Endpoint, authored["endpoint"])
	}
}

func TestProjectDeploymentAddAfterCustomSubdomainAuthoring(t *testing.T) {
	project := resolvedCustomSubdomainProject(t)
	accountID := strings.TrimSuffix(project.ResourceId, "/projects/project")
	for _, tt := range []struct {
		name, endpoint, advertisedEndpoint, wantErr string
		projectStatus, accountStatus                int
		wantReads                                   int
		capacity                                    int32
		malformedDeployments, malformedBindings     bool
		missingAdvertisedEndpoint                   bool
	}{
		{name: "custom subdomain", wantReads: 2},
		{name: "legacy project path", endpoint: "https://custom-dns.services.ai.azure.com/projects/project",
			wantReads: 2},
		{name: "different project", endpoint: "https://custom-dns.services.ai.azure.com/api/projects/other",
			wantErr: "identify different projects"},
		{name: "different account with the same project name",
			endpoint: "https://other.services.ai.azure.com/api/projects/project",
			wantErr:  "identify different projects", wantReads: 2},
		{name: "stale account-name endpoint",
			endpoint: "https://resource-account.services.ai.azure.com/api/projects/project",
			wantErr:  "identify different projects", wantReads: 2},
		{name: "project read denied", projectStatus: http.StatusForbidden,
			wantErr: "AuthorizationFailed", wantReads: 1},
		{name: "account read denied uses advertised endpoint", accountStatus: http.StatusForbidden, wantReads: 2},
		{name: "account read denied without advertised endpoint", accountStatus: http.StatusForbidden,
			missingAdvertisedEndpoint: true, wantErr: "AuthorizationFailed", wantReads: 2},
		{name: "account read denied with wrong project advertisement", accountStatus: http.StatusForbidden,
			advertisedEndpoint: "https://custom-dns.services.ai.azure.com/api/projects/other",
			wantErr:            "different project", wantReads: 2},
		{name: "project missing", projectStatus: http.StatusNotFound, wantErr: "ResourceNotFound", wantReads: 1},
		{name: "account missing", accountStatus: http.StatusNotFound, wantErr: "ResourceNotFound", wantReads: 2},
		{name: "invalid advertised endpoint", advertisedEndpoint: "https://example.com/api/projects/project",
			wantErr: "invalid ARM-advertised Foundry project endpoint", wantReads: 2},
		{name: "negative capacity before lookup", capacity: -1, wantErr: "deployment capacity"},
		{name: "malformed deployments before lookup", malformedDeployments: true, wantErr: "AI_PROJECT_DEPLOYMENTS"},
		{name: "malformed bindings before lookup", malformedBindings: true, wantErr: "AI_PROJECT_DEPLOYMENT_BINDINGS"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)
			raw := []byte("name: test\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), raw, 0o600))
			client, projectServer, environment, aiServer, _ := newSelfInitializingDeploymentClient(t, root)
			projectServer.project.Services["project"] = &azdext.ServiceConfig{Name: "project", Host: aiProjectHost}
			projectServer.services["project"] = map[string]any{}
			reconciler := &projectServiceReconciler{client: client, projectRoot: root}
			_, _, _, err := reconciler.reconcileEndpoint(t.Context(), "project", project.Endpoint, project.Mode)
			require.NoError(t, err)
			_, _, err = reconcileProjectEnvironmentWithRollback(t.Context(), client, "test", project.Mode, project, false)
			require.NoError(t, err)
			require.Equal(t, project.Endpoint, projectServer.services["project"]["endpoint"])
			require.Equal(t, project.ResourceId, environment.values["AZURE_AI_PROJECT_ID"])
			if tt.endpoint != "" {
				projectServer.services["project"]["endpoint"] = tt.endpoint
			}
			if tt.malformedDeployments {
				environment.values[projectDeploymentsEnvKey] = "{"
			}
			if tt.malformedBindings {
				environment.values[synthesis.DeploymentBindingsEnvKey] = "{"
			}
			beforeEnv := maps.Clone(environment.values)
			beforeService := mustProjectProperties(t, projectServer.services["project"]).AsMap()
			environment.setLog = nil
			var reads []string
			lookup := func(ctx context.Context, actualClient *azdext.AzdClient, id string) (*resolvedProject, error) {
				require.Same(t, client, actualClient)
				require.Equal(t, project.ResourceId, id)
				selected, err := projectFromResourceID(id)
				require.NoError(t, err)
				return hydrateResolvedProject(ctx, selected, endpointTestCredential{},
					&arm.ClientOptions{ClientOptions: policy.ClientOptions{
						Retry: policy.RetryOptions{MaxRetries: -1},
						Transport: endpointTestTransport(func(request *http.Request) (*http.Response, error) {
							reads = append(reads, request.URL.Path)
							statusCode := tt.projectStatus
							advertised := tt.advertisedEndpoint
							if advertised == "" {
								advertised = project.Endpoint
							}
							body := `{"properties":{"endpoints":{"AI Foundry API":"` + advertised + `"}}}`
							if request.URL.Path == accountID {
								statusCode = tt.accountStatus
								body = `{"properties":{"customSubDomainName":"custom-dns"}}`
							} else {
								require.Equal(t, project.ResourceId, request.URL.Path)
								if tt.missingAdvertisedEndpoint {
									body = `{"properties":{}}`
								}
							}
							if statusCode == 0 {
								statusCode = http.StatusOK
							}
							if statusCode == http.StatusForbidden {
								body = `{"error":{"code":"AuthorizationFailed","message":"access denied"}}`
							} else if statusCode == http.StatusNotFound {
								body = `{"error":{"code":"ResourceNotFound","message":"resource missing"}}`
							}
							return &http.Response{
								StatusCode: statusCode, Header: http.Header{"Content-Type": {"application/json"}},
								Body: io.NopCloser(strings.NewReader(body)), Request: request,
							}, nil
						}),
					}})
			}
			action := &ProjectDeploymentAddAction{
				client: client,
				flags: &projectDeploymentFlags{
					model: "OpenAI/gpt-4.1", name: "chat", capacity: tt.capacity, output: "none",
				},
				extCtx: &azdext.ExtensionContext{Environment: "test", NoPrompt: true},
			}
			err = action.runWithProjectLookup(t.Context(), lookup)
			require.Len(t, reads, tt.wantReads)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Equal(t, beforeEnv, environment.values)
				require.Equal(t, beforeService, mustProjectProperties(t, projectServer.services["project"]).AsMap())
				require.Empty(t, environment.setLog)
				require.False(t, projectServer.deploymentSet)
				require.Zero(t, aiServer.calls)
			} else {
				require.NoError(t, err)
				require.Equal(t, []string{project.ResourceId, accountID}, reads)
				require.True(t, projectServer.deploymentSet)
				require.Equal(t, 1, aiServer.calls)
				require.Equal(t, project.ResourceId, environment.values["AZURE_AI_PROJECT_ID"])
				require.Equal(t, project.Endpoint, environment.values["FOUNDRY_PROJECT_ENDPOINT"])
				require.Equal(t, "chat", environment.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
				deployments, present, err := provisioning.DecodeProjectDeployments(
					environment.values[projectDeploymentsEnvKey])
				require.NoError(t, err)
				require.True(t, present)
				require.Equal(t, []synthesis.Deployment{{
					Name:  "chat",
					Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4.1", Version: "2025-04-14"},
					Sku:   synthesis.DeploymentSku{Name: "GlobalStandard", Capacity: 1},
				}}, deployments)
			}
			// #nosec G304 -- path is under the test-owned root.
			after, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, raw, after)
		})
	}
}

func TestProjectReplacementAfterAgentsEnvironmentSelection(t *testing.T) {
	project := resolvedCustomSubdomainProject(t)
	for _, tt := range []struct {
		name, serviceEndpoint, savedID string
		replaced                       bool
	}{
		{name: "same resolved project", serviceEndpoint: project.Endpoint},
		{name: "repair legacy endpoint",
			serviceEndpoint: "https://resource-account.services.ai.azure.com/api/projects/project"},
		{name: "same resolved project with a legacy path",
			serviceEndpoint: "https://custom-dns.services.ai.azure.com/projects/project"},
		{name: "repair legacy endpoint with a legacy path",
			serviceEndpoint: "https://resource-account.services.ai.azure.com/projects/project"},
		{name: "different account with the same project name",
			serviceEndpoint: "https://old-account.services.ai.azure.com/api/projects/project", replaced: true},
		{name: "different project",
			serviceEndpoint: "https://custom-dns.services.ai.azure.com/api/projects/old-project", replaced: true},
		{name: "different ARM identity with the same endpoint", serviceEndpoint: project.Endpoint,
			savedID: strings.Replace(project.ResourceId, "/rg/", "/old-rg/", 1), replaced: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{
				"AZURE_AI_PROJECT_ID":      project.ResourceId,
				"AZURE_AI_ACCOUNT_NAME":    project.AccountName,
				"AZURE_AI_PROJECT_NAME":    project.ProjectName,
				"FOUNDRY_PROJECT_ENDPOINT": project.Endpoint,
			}
			if tt.savedID != "" {
				values["AZURE_AI_PROJECT_ID"] = tt.savedID
			}
			for _, key := range projectReplacementEnvironmentKeys {
				values[key] = "old-" + key
			}
			before := maps.Clone(values)
			environment := &transactionEnvironmentServer{values: values}
			client := newProjectEnvironmentClient(t, environment)
			service := &projectServiceInfo{Resolved: map[string]any{"endpoint": tt.serviceEndpoint}}
			flags := &projectAddFlags{projectID: project.ResourceId, noPrompt: true}
			err := confirmExplicitProjectReplacement(t.Context(), nil, project, service, values, flags)
			if tt.replaced {
				require.ErrorContains(t, err, "the explicit project target differs")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, before, values)
			flags.force = true
			require.NoError(t, confirmExplicitProjectReplacement(t.Context(), nil, project, service, values, flags))
			replaced := projectIdentityChanged(values, serviceEndpoint(service.Resolved), project)
			require.Equal(t, tt.replaced, replaced)
			_, _, err = reconcileProjectEnvironmentWithRollback(t.Context(), client, "test",
				project.Mode, project, replaced)
			require.NoError(t, err)
			for _, key := range projectReplacementEnvironmentKeys {
				if tt.replaced {
					require.Empty(t, values[key], key)
				} else {
					require.Equal(t, before[key], values[key], key)
				}
			}
			require.Equal(t, project.ResourceId, values["AZURE_AI_PROJECT_ID"])
			require.Equal(t, project.Endpoint, values["FOUNDRY_PROJECT_ENDPOINT"])
		})
	}
}

func TestProjectEjectIdentityUsesResolvedARMEndpoint(t *testing.T) {
	project := resolvedCustomSubdomainProject(t)
	identity, err := projectEjectIdentity(project.Endpoint, project, nil)
	require.NoError(t, err)
	require.Equal(t, project.ResourceId, identity.ResourceId)
	require.Equal(t, project.Endpoint, identity.Endpoint)
	require.Equal(t, project.OpenAIEndpoint, identity.OpenAIEndpoint)

	_, err = projectEjectIdentity("https://other-account.services.ai.azure.com/api/projects/project", project, nil)
	require.ErrorContains(t, err, "does not match the project resource ID")

	replacement := *project
	replacement.ResourceId = strings.Replace(project.ResourceId, "/accounts/resource-account/",
		"/accounts/other-account/", 1)
	require.True(t, projectIdentityChanged(map[string]string{"AZURE_AI_PROJECT_ID": project.ResourceId},
		project.Endpoint, &replacement))
}

func TestProjectCustomSubdomainInfrastructureEjection(t *testing.T) {
	project := resolvedCustomSubdomainProject(t)
	for _, provider := range []string{"bicep", "terraform"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			raw := "name: test\nservices:\n  project:\n    host: azure.ai.project\n" +
				"    endpoint: " + project.Endpoint + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(raw), 0o600))
			server := &transactionProjectServer{project: &azdext.ProjectConfig{
				Name: "test", Path: root, Infra: &azdext.InfraOptions{Provider: provisioningFoundryProvider},
			}}
			client := newTransactionProjectClient(t, server)
			require.NoError(t, ejectProjectInfraWithTarget(t.Context(), client, root, "project", provider, project))
			parameterFile := "main.tfvars.json"
			if provider == "bicep" {
				parameterFile = "main.parameters.json"
			}
			// #nosec G304 -- path is under the test-owned root.
			parameters, err := os.ReadFile(filepath.Join(root, "infra", parameterFile))
			require.NoError(t, err)
			var values struct {
				ID         string `json:"project_resource_id"`
				Endpoint   string `json:"project_endpoint"`
				Parameters struct {
					ID       struct{ Value string } `json:"projectResourceId"`
					Endpoint struct{ Value string } `json:"projectEndpoint"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(parameters, &values))
			if provider == "bicep" {
				require.Equal(t, "${AZURE_AI_PROJECT_ID}", values.Parameters.ID.Value)
				require.Equal(t, "${FOUNDRY_PROJECT_ENDPOINT}", values.Parameters.Endpoint.Value)
				if bicep := os.Getenv("AZD_BICEP_TOOL_PATH"); bicep != "" {
					//nolint:gosec // Explicit Bicep test override.
					command := exec.CommandContext(t.Context(), bicep, "build",
						filepath.Join(root, "infra", "main.bicep"), "--outfile", filepath.Join(root, "compiled.json"))
					output, err := command.CombinedOutput()
					require.NoError(t, err, "%s", output)
				}
			} else {
				require.Equal(t, "${AZURE_AI_PROJECT_ID}", values.ID)
				require.Equal(t, "${FOUNDRY_PROJECT_ENDPOINT}", values.Endpoint)
			}
		})
	}
}
