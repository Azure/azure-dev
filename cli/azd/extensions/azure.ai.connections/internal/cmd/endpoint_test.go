// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"testing"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/pkg/connections"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveARMContext_PrefersEnvProjectID(t *testing.T) {
	t.Parallel()

	const projectID = "/subscriptions/sub-123/resourceGroups/rg-abc/providers/" +
		"Microsoft.CognitiveServices/accounts/cog-xyz/projects/proj-1"
	for _, host := range []string{"cog-xyz", "custom-dns"} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			endpoint := "https://" + host + ".services.ai.azure.com/api/projects/proj-1"
			calls := 0
			options := identityClientOptions(deleteReadinessTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, http.MethodGet, request.Method)
				assert.Equal(t, "management.azure.com", request.URL.Host)
				assert.Equal(t, projectID, request.URL.Path)
				assert.Equal(t, "2025-06-01", request.URL.Query().Get("api-version"))
				return identityResponse(request, http.StatusOK,
					`{"properties":{"endpoints":{"AI Foundry API":"`+endpoint+`"}}}`), nil
			}))

			armCtx, err := resolveARMContext(t.Context(), projectID, endpoint, nil, deleteReadinessCredential{}, options)
			require.NoError(t, err)
			assert.Equal(t, 1, calls, "verify metadata even when DNS and ARM names are identical")
			assert.Equal(t, &armContext{
				SubscriptionID: "sub-123", ResourceGroup: "rg-abc", AccountName: "cog-xyz", ProjectName: "proj-1",
			}, armCtx)
		})
	}
}

func TestResolveARMContext_MatchIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	projectID := "/subscriptions/sub-123/resourceGroups/rg-abc/providers/" +
		"Microsoft.CognitiveServices/accounts/Cog-XYZ/projects/Proj-1"
	options := identityClientOptions(deleteReadinessTransport(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, projectID, request.URL.Path)
		return identityResponse(request, http.StatusOK,
				`{"properties":{"endpoints":{"AI Foundry API":"https://CUSTOM-DNS.services.ai.azure.com/api/projects/Proj-1///"}}}`),
			nil
	}))

	armCtx, err := resolveARMContext(t.Context(), projectID,
		"https://custom-dns.services.ai.azure.com/api/projects/proj-1", nil, deleteReadinessCredential{}, options)
	require.NoError(t, err)
	assert.Equal(t, "Cog-XYZ", armCtx.AccountName)
	assert.Equal(t, "Proj-1", armCtx.ProjectName)
}

func TestResolveARMContext_EmptyProjectRequiresResourceID(t *testing.T) {
	t.Parallel()

	const endpoint = "https://secret-user:secret-password@private-account.services.ai.azure.com/" +
		"api/projects/private-project?sig=secret-signature#secret-fragment"
	lister := &endpointConnectionLister{}

	armCtx, err := resolveARMContext(t.Context(), "", endpoint, lister, nil, nil)
	require.Error(t, err)
	assert.Nil(t, armCtx)
	assert.Equal(t, 1, lister.calls)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	assert.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
	assert.Contains(t, localErr.Message, "AZURE_AI_PROJECT_ID is required")
	assert.Contains(t, localErr.Message, "project has no connections")
	assert.Contains(t, localErr.Suggestion, "full ARM resource ID of the project matching the endpoint")
	assert.Contains(t, localErr.Suggestion, "selected azd environment (not only in the shell)")
	assert.Contains(t, localErr.Suggestion,
		"azd env set AZURE_AI_PROJECT_ID \"<project-resource-id>\" --environment \"<environment>\"")
	assert.Contains(t, localErr.Suggestion,
		"azd ai project add --project-id \"<project-resource-id>\" --environment \"<environment>\"")
	assert.Contains(t, localErr.Suggestion, "retry the Connection command or deployment that produced this error")
	assert.NotContains(t, localErr.Suggestion, "azd deploy")
	for _, sensitive := range []string{
		endpoint, "private-account", "private-project", "secret-user", "secret-password", "secret-signature", "secret-fragment",
	} {
		assert.NotContains(t, err.Error()+localErr.Suggestion, sensitive)
	}
}

func TestResolveARMContext_DiscoveryFallback(t *testing.T) {
	t.Parallel()

	const projectIDPrefix = "/subscriptions/other-sub/resourceGroups/other-rg/providers/" +
		"Microsoft.CognitiveServices/accounts/"
	for _, tt := range []struct {
		name          string
		projectID     string
		metadata      string
		status        int
		metadataCalls int
	}{
		{name: "missing project ID"},
		{name: "malformed project ID", projectID: "not-an-arm-resource-id"},
		{name: "wrong provider", projectID: strings.ReplaceAll(identityProjectID, "Microsoft.CognitiveServices", "Other")},
		{
			name: "different account", projectID: projectIDPrefix + "other-account/projects/proj-1",
			metadata:      `{"properties":{"endpoints":{"AI Foundry API":"https://other.services.ai.azure.com/api/projects/proj-1"}}}`,
			metadataCalls: 1,
		},
		{
			name: "same account and project in another scope", projectID: projectIDPrefix + "cog-xyz/projects/proj-1",
			metadata:      `{"properties":{"endpoints":{"AI Foundry API":"https://other.services.ai.azure.com/api/projects/proj-1"}}}`,
			metadataCalls: 1,
		},
		{name: "different project", projectID: projectIDPrefix + "cog-xyz/projects/other-project"},
		{
			name: "project no longer exists", projectID: projectIDPrefix + "cog-xyz/projects/proj-1",
			status: http.StatusNotFound, metadataCalls: 1,
		},
		{
			name: "missing advertised endpoint", projectID: projectIDPrefix + "cog-xyz/projects/proj-1",
			metadata: `{"properties":{"endpoints":{}}}`, metadataCalls: 1,
		},
		{
			name: "missing properties", projectID: projectIDPrefix + "cog-xyz/projects/proj-1",
			metadata: `{}`, metadataCalls: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lister := &endpointConnectionLister{
				connections: []connections.Connection{{
					ID: "/subscriptions/sub-123/resourceGroups/rg-abc/providers/" +
						"Microsoft.CognitiveServices/accounts/cog-xyz/projects/proj-1/connections/existing",
				}},
			}
			metadataCalls := 0
			options := identityClientOptions(deleteReadinessTransport(func(request *http.Request) (*http.Response, error) {
				metadataCalls++
				assert.Equal(t, "management.azure.com", request.URL.Host)
				status := tt.status
				if status == 0 {
					status = http.StatusOK
				}
				return identityResponse(request, status, tt.metadata), nil
			}))

			armCtx, err := resolveARMContext(t.Context(), tt.projectID,
				"https://custom-dns.services.ai.azure.com/api/projects/proj-1",
				lister, deleteReadinessCredential{}, options)
			require.NoError(t, err)
			assert.Equal(t, tt.metadataCalls, metadataCalls)
			assert.Equal(t, 1, lister.calls)
			assert.Equal(t, &armContext{
				SubscriptionID: "sub-123",
				ResourceGroup:  "rg-abc",
				AccountName:    "cog-xyz",
				ProjectName:    "proj-1",
			}, armCtx)
		})
	}
}

func TestResolveARMContext_ConflictingProjectWithoutConnections(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		projectID string
		metadata  string
	}{
		{name: "different project", projectID: strings.ReplaceAll(identityProjectID, "project-one", "other-project")},
		{
			name: "different account", projectID: strings.ReplaceAll(identityProjectID, "arm-account", "other-account"),
			metadata: `{"properties":{"endpoints":{"AI Foundry API":"https://other.services.ai.azure.com/api/projects/project-one"}}}`,
		},
		{
			name: "same names but unrelated scope", projectID: strings.ReplaceAll(identityProjectID, "staging-rg", "other-rg"),
			metadata: `{"properties":{"endpoints":{"AI Foundry API":"https://other.services.ai.azure.com/api/projects/project-one"}}}`,
		},
		{name: "missing advertised endpoint", projectID: identityProjectID, metadata: `{"properties":{"endpoints":{}}}`},
		{
			name: "another endpoint key is not authoritative", projectID: identityProjectID,
			metadata: `{"properties":{"endpoints":{"OpenAI":"` + identityEndpoint + `"}}}`,
		},
		{
			name: "same host but another project", projectID: identityProjectID,
			metadata: `{"properties":{"endpoints":{"AI Foundry API":"https://custom-dns.services.ai.azure.com/api/projects/other"}}}`,
		},
		{
			name: "invalid advertised endpoint", projectID: identityProjectID,
			metadata: `{"properties":{"endpoints":{"AI Foundry API":"http://custom-dns.services.ai.azure.com/api/projects/project-one"}}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lister := &endpointConnectionLister{}
			options := identityClientOptions(deleteReadinessTransport(func(request *http.Request) (*http.Response, error) {
				return identityResponse(request, http.StatusOK, tt.metadata), nil
			}))
			resolved, err := resolveARMContext(t.Context(), tt.projectID, identityEndpoint,
				lister, deleteReadinessCredential{}, options)
			require.Error(t, err)
			assert.Nil(t, resolved)
			assert.Equal(t, 1, lister.calls)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
			assert.Contains(t, localErr.Message, "AZURE_AI_PROJECT_ID")
			assert.Contains(t, localErr.Suggestion, "--project-endpoint")
			assert.Contains(t, localErr.Suggestion, "selected azd environment")
		})
	}
}

func TestResolveARMContext_MetadataFailuresAreNotHidden(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests,
		http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			lister := &endpointConnectionLister{}
			options := identityClientOptions(deleteReadinessTransport(func(request *http.Request) (*http.Response, error) {
				return identityResponse(request, status,
					`{"error":{"code":"MetadataUnavailable","message":"project metadata is unavailable"}}`), nil
			}))
			resolved, err := resolveARMContext(t.Context(), identityProjectID, identityEndpoint,
				lister, deleteReadinessCredential{}, options)
			require.Error(t, err)
			assert.Nil(t, resolved)
			serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
			require.True(t, ok)
			assert.Equal(t, status, serviceErr.StatusCode)
			assert.Equal(t, "get_project", serviceErr.ServiceName)
			assert.Equal(t, "MetadataUnavailable", serviceErr.ErrorCode)
			assert.Zero(t, lister.calls)
		})
	}
	for _, failure := range []error{context.Canceled, errors.New("synthetic transport failure")} {
		t.Run(failure.Error(), func(t *testing.T) {
			t.Parallel()
			lister := &endpointConnectionLister{}
			options := identityClientOptions(deleteReadinessTransport(func(*http.Request) (*http.Response, error) {
				return nil, failure
			}))
			resolved, err := resolveARMContext(t.Context(), identityProjectID, identityEndpoint,
				lister, deleteReadinessCredential{}, options)
			require.ErrorIs(t, err, failure)
			assert.Nil(t, resolved)
			assert.Zero(t, lister.calls)
		})
	}
}

func TestResolveARMContext_DiscoveryRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()
	for _, id := range []string{
		"not-an-arm-resource-id",
		strings.ReplaceAll(identityProjectID, "Microsoft.CognitiveServices", "Other"),
		strings.ReplaceAll(identityProjectID, "project-one", "other-project"),
	} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			lister := &endpointConnectionLister{connections: []connections.Connection{{ID: id + "/connections/existing"}}}
			resolved, err := resolveARMContext(t.Context(), "", identityEndpoint, lister, nil, nil)
			require.Error(t, err)
			assert.Nil(t, resolved)
			assert.Equal(t, 1, lister.calls)
		})
	}
	failure := errors.New("synthetic discovery failure")
	lister := &endpointConnectionLister{err: failure}
	resolved, err := resolveARMContext(t.Context(), "", identityEndpoint, lister, nil, nil)
	require.ErrorIs(t, err, failure)
	assert.Nil(t, resolved)
}

func TestResolveARMContext_DoesNotDiscloseURLCredentials(t *testing.T) {
	var logs bytes.Buffer
	writer := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(writer) })
	const sensitiveURL = "https://secret-user:secret-password@private-account.services.ai.azure.com/" +
		"api/projects/project-one?sig=secret-signature#secret-fragment"
	for _, tt := range []struct {
		projectID string
		endpoint  string
	}{
		{projectID: sensitiveURL, endpoint: identityEndpoint},
		{projectID: identityProjectID, endpoint: identityEndpoint},
		{endpoint: strings.ReplaceAll(sensitiveURL, "project-one", "%zz")},
	} {
		logs.Reset()
		lister := &endpointConnectionLister{}
		options := identityClientOptions(deleteReadinessTransport(func(request *http.Request) (*http.Response, error) {
			return identityResponse(request, http.StatusOK,
				`{"properties":{"endpoints":{"AI Foundry API":"`+sensitiveURL+`"}}}`), nil
		}))
		_, err := resolveARMContext(t.Context(), tt.projectID, tt.endpoint, lister, deleteReadinessCredential{}, options)
		require.Error(t, err)
		output := logs.String() + err.Error()
		if localErr, ok := errors.AsType[*azdext.LocalError](err); ok {
			output += localErr.Suggestion
		}
		for _, secret := range []string{"secret-user", "secret-password", "secret-signature", "secret-fragment", sensitiveURL} {
			assert.NotContains(t, output, secret)
		}
	}
}

type endpointConnectionLister struct {
	connections []connections.Connection
	calls       int
	err         error
}

func (l *endpointConnectionLister) ListConnections(context.Context) ([]connections.Connection, error) {
	l.calls++
	return l.connections, l.err
}
