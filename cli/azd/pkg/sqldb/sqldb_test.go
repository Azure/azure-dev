// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package sqldb

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql/v2"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
)

func TestNewSqlDbService(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	svc, err := NewSqlDbService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
}

func TestConnectionString_CredentialError(t *testing.T) {
	expectedErr := errors.New("credential failure")
	credProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return nil, expectedErr
		},
	)

	svc, err := NewSqlDbService(credProvider, nil)
	require.NoError(t, err)

	_, err = svc.ConnectionString(
		t.Context(), "sub-id", "rg", "server", "db",
	)
	require.ErrorIs(t, err, expectedErr)
}

func TestConnectionString_ServerNotFound(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path, "Microsoft.Sql/servers",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := map[string]any{
			"error": map[string]any{
				"code":    "ResourceNotFound",
				"message": "Server not found",
			},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusNotFound, body,
		)
	})

	svc, err := NewSqlDbService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NoError(t, err)

	_, err = svc.ConnectionString(
		t.Context(), "sub-id", "rg", "missing-server", "",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed getting server")
}

func TestConnectionString_NilProperties(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path, "Microsoft.Sql/servers",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armsql.Server{Properties: nil}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc, err := NewSqlDbService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NoError(t, err)

	_, err = svc.ConnectionString(
		t.Context(), "sub-id", "rg", "myserver", "",
	)
	require.Error(t, err)
	require.Contains(
		t, err.Error(), "failed getting server properties",
	)
}

func TestConnectionString_NilFQDN(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path, "Microsoft.Sql/servers",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armsql.Server{
			Properties: &armsql.ServerProperties{
				FullyQualifiedDomainName: nil,
			},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc, err := NewSqlDbService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NoError(t, err)

	_, err = svc.ConnectionString(
		t.Context(), "sub-id", "rg", "myserver", "",
	)
	require.Error(t, err)
	require.Contains(
		t, err.Error(), "failed getting fully qualified domain name",
	)
}

func TestConnectionString_EmptyFQDN(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(
				request.URL.Path, "Microsoft.Sql/servers",
			)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		body := armsql.Server{
			Properties: &armsql.ServerProperties{
				FullyQualifiedDomainName: new(""),
			},
		}
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, body,
		)
	})

	svc, err := NewSqlDbService(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
	)
	require.NoError(t, err)

	_, err = svc.ConnectionString(
		t.Context(), "sub-id", "rg", "myserver", "",
	)
	require.Error(t, err)
	require.Contains(
		t, err.Error(), "failed getting fully qualified domain name",
	)
}

func TestConnectionString_Success(t *testing.T) {
	fqdn := "myserver.database.windows.net"

	tests := []struct {
		name     string
		dbName   string
		contains string
		excludes string
	}{
		{
			name:     "without database name",
			dbName:   "",
			contains: "Server=tcp:" + fqdn + ",1433;",
			excludes: "Initial Catalog",
		},
		{
			name:     "with database name",
			dbName:   "mydb",
			contains: "Initial Catalog=mydb;",
			excludes: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCtx := mocks.NewMockContext(t.Context())

			mockCtx.HttpClient.When(func(req *http.Request) bool {
				return req.Method == http.MethodGet &&
					strings.Contains(
						req.URL.Path, "Microsoft.Sql/servers",
					)
			}).RespondFn(func(req *http.Request) (*http.Response, error) {
				body := armsql.Server{
					Properties: &armsql.ServerProperties{
						FullyQualifiedDomainName: new(fqdn),
					},
				}
				return mocks.CreateHttpResponseWithBody(
					req, http.StatusOK, body,
				)
			})

			svc, err := NewSqlDbService(
				mockCtx.SubscriptionCredentialProvider,
				mockCtx.ArmClientOptions,
			)
			require.NoError(t, err)

			connStr, err := svc.ConnectionString(
				t.Context(), "sub", "rg", "myserver", tt.dbName,
			)
			require.NoError(t, err)
			require.Contains(t, connStr, tt.contains)
			require.Contains(t, connStr, "Encrypt=True")
			require.Contains(
				t, connStr, `Authentication="Active Directory Default"`,
			)
			if tt.excludes != "" {
				require.NotContains(t, connStr, tt.excludes)
			}
		})
	}
}
