// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package keyvault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// kvChallengeHeader returns a WWW-Authenticate Bearer challenge that satisfies
// the azsecrets challenge-auth policy so tests can exercise the data-plane
// client without a real vault.
func kvChallengeHeader(resource string) string {
	return `Bearer authorization="https://login.microsoftonline.com/common/oauth2/authorize", resource="` + resource + `"`
}

// writeJSON serializes v as JSON and returns an *http.Response with it as body.
func writeJSON(request *http.Request, status int, v any) *http.Response {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Request:    request,
		Body:       io.NopCloser(bytes.NewBuffer(b)),
	}
}

// withChallengeAuth wraps a RespondFn so that the first, unauthenticated call
// receives a 401 + WWW-Authenticate header and subsequent authenticated calls
// are forwarded to the real handler.
func withChallengeAuth(resource string, inner mockhttp.RespondFn) mockhttp.RespondFn {
	return func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") == "" {
			resp := &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     http.Header{},
				Request:    req,
				Body:       http.NoBody,
			}
			resp.Header.Set("WWW-Authenticate", kvChallengeHeader(resource))
			return resp, nil
		}
		return inner(req)
	}
}

// newTestService constructs a keyVaultService wired to a MockHttpClient
// transport. The returned *keyVaultService can be used as a KeyVaultService
// because it satisfies the interface.
func newTestService(mockHttp *mockhttp.MockHttpClient) *keyVaultService {
	armOpts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: mockHttp,
		},
	}
	coreOpts := &azcore.ClientOptions{
		Transport: mockHttp,
	}
	return &keyVaultService{
		credentialProvider: &mocks.MockSubscriptionCredentialProvider{},
		armClientOptions:   armOpts,
		coreClientOptions:  coreOpts,
		cloud:              cloud.AzurePublic(),
	}
}

// ---------------------------------------------------------------------------
// NewKeyVaultService (constructor) — smoke test
// ---------------------------------------------------------------------------

func TestNewKeyVaultService(t *testing.T) {
	t.Parallel()

	svc := NewKeyVaultService(
		&mocks.MockSubscriptionCredentialProvider{},
		&arm.ClientOptions{},
		&azcore.ClientOptions{},
		cloud.AzurePublic(),
	)
	require.NotNil(t, svc)
	// Service should satisfy the public interface.
	var _ KeyVaultService = svc
}

// ---------------------------------------------------------------------------
// GetKeyVault (ARM control-plane)
// ---------------------------------------------------------------------------

func TestKeyVaultService_GetKeyVault(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		vaultID := "/subscriptions/sub-1/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/myvault"
		mockHttp.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/vaults/myvault")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			body := map[string]any{
				"id":       vaultID,
				"name":     "myvault",
				"location": "eastus2",
				"properties": map[string]any{
					"enableSoftDelete":      true,
					"enablePurgeProtection": false,
				},
			}
			return writeJSON(req, http.StatusOK, body), nil
		})

		svc := newTestService(mockHttp)
		kv, err := svc.GetKeyVault(t.Context(), "sub-1", "rg1", "myvault")
		require.NoError(t, err)
		assert.Equal(t, "myvault", kv.Name)
		assert.Equal(t, "eastus2", kv.Location)
		assert.Equal(t, vaultID, kv.Id)
		assert.True(t, kv.Properties.EnableSoftDelete)
		assert.False(t, kv.Properties.EnablePurgeProtection)
	})

	t.Run("not found returns error", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/vaults/missing")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		})

		svc := newTestService(mockHttp)
		_, err := svc.GetKeyVault(t.Context(), "sub-1", "rg1", "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting key vault")
	})
}

// ---------------------------------------------------------------------------
// GetKeyVaultSecret (data-plane)
// ---------------------------------------------------------------------------

func TestKeyVaultService_GetKeyVaultSecret(t *testing.T) {
	t.Parallel()

	t.Run("success returns secret value", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		secretValue := "super-secret"
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/mysecret") && req.Method == http.MethodGet
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			body := map[string]any{
				"id":    "https://myvault.vault.azure.net/secrets/mysecret/ver1",
				"value": secretValue,
			}
			return writeJSON(req, http.StatusOK, body), nil
		}))

		svc := newTestService(mockHttp)
		secret, err := svc.GetKeyVaultSecret(t.Context(), "sub-1", "myvault", "mysecret")
		require.NoError(t, err)
		assert.Equal(t, "mysecret", secret.Name)
		assert.Equal(t, secretValue, secret.Value)
		assert.Equal(t, "ver1", secret.Id)
	})

	t.Run("not found returns sentinel error", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		}))

		svc := newTestService(mockHttp)
		_, err := svc.GetKeyVaultSecret(t.Context(), "sub-1", "myvault", "missing")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrAzCliSecretNotFound)
	})

	t.Run("server error is wrapped", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusInternalServerError)
		}))

		svc := newTestService(mockHttp)
		_, err := svc.GetKeyVaultSecret(t.Context(), "sub-1", "myvault", "broken")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting key vault secret")
		assert.False(t, errors.Is(err, ErrAzCliSecretNotFound))
	})

	t.Run("vault name already a URL is accepted", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/mysecret")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			body := map[string]any{
				"id":    "https://myvault.vault.azure.net/secrets/mysecret/v1",
				"value": "v",
			}
			return writeJSON(req, http.StatusOK, body), nil
		}))

		svc := newTestService(mockHttp)
		// Pass a full URL as the "vaultName" to exercise the branch in
		// createSecretsDataClient that skips prepending https://.
		secret, err := svc.GetKeyVaultSecret(
			t.Context(), "sub-1", "https://myvault.vault.azure.net", "mysecret")
		require.NoError(t, err)
		assert.Equal(t, "v", secret.Value)
	})
}

// ---------------------------------------------------------------------------
// CreateKeyVaultSecret (data-plane)
// ---------------------------------------------------------------------------

func TestKeyVaultService_CreateKeyVaultSecret(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return req.Method == http.MethodPut &&
				strings.Contains(req.URL.Path, "/secrets/mysecret")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			// Parse body to ensure the SDK serialized the value correctly.
			b, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			var parsed map[string]any
			_ = json.Unmarshal(b, &parsed)
			body := map[string]any{
				"id":    "https://myvault.vault.azure.net/secrets/mysecret/v1",
				"value": parsed["value"],
			}
			return writeJSON(req, http.StatusOK, body), nil
		}))

		svc := newTestService(mockHttp)
		err := svc.CreateKeyVaultSecret(t.Context(), "sub-1", "myvault", "mysecret", "s3cret")
		require.NoError(t, err)
	})

	t.Run("server error is returned", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusForbidden)
		}))

		svc := newTestService(mockHttp)
		err := svc.CreateKeyVaultSecret(t.Context(), "sub-1", "myvault", "mysecret", "val")
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// ListKeyVaultSecrets (data-plane pager)
// ---------------------------------------------------------------------------

func TestKeyVaultService_ListKeyVaultSecrets(t *testing.T) {
	t.Parallel()

	t.Run("success single page", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.HasSuffix(req.URL.Path, "/secrets")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			body := map[string]any{
				"value": []map[string]any{
					{"id": "https://myvault.vault.azure.net/secrets/alpha"},
					{"id": "https://myvault.vault.azure.net/secrets/beta"},
				},
			}
			return writeJSON(req, http.StatusOK, body), nil
		}))

		svc := newTestService(mockHttp)
		names, err := svc.ListKeyVaultSecrets(t.Context(), "sub-1", "myvault")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"alpha", "beta"}, names)
	})

	t.Run("error response yields wrapped error", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.HasSuffix(req.URL.Path, "/secrets")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusInternalServerError)
		}))

		svc := newTestService(mockHttp)
		_, err := svc.ListKeyVaultSecrets(t.Context(), "sub-1", "myvault")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "listing key vault secrets")
	})
}

// ---------------------------------------------------------------------------
// ListSubscriptionVaults (ARM pager)
// ---------------------------------------------------------------------------

func TestKeyVaultService_ListSubscriptionVaults(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		// The VaultsClient.NewListPager actually queries the generic
		// /subscriptions/{sub}/resources?$filter=resourceType eq 'Microsoft.KeyVault/vaults'
		// endpoint, so we match on /resources here.
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/resources")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			body := map[string]any{
				"value": []map[string]any{
					{
						"id":   "/subscriptions/sub/providers/Microsoft.KeyVault/vaults/v1",
						"name": "v1",
					},
					{
						"id":   "/subscriptions/sub/providers/Microsoft.KeyVault/vaults/v2",
						"name": "v2",
					},
				},
			}
			return writeJSON(req, http.StatusOK, body), nil
		})

		svc := newTestService(mockHttp)
		vaults, err := svc.ListSubscriptionVaults(t.Context(), "sub-1")
		require.NoError(t, err)
		require.Len(t, vaults, 2)
		assert.Equal(t, "v1", vaults[0].Name)
		assert.Equal(t, "v2", vaults[1].Name)
	})

	t.Run("pager error wrapped", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		// Return an error that also hits the provider-registration retry path.
		mockHttp.When(func(req *http.Request) bool {
			return true
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusInternalServerError)
		})

		svc := newTestService(mockHttp)
		_, err := svc.ListSubscriptionVaults(t.Context(), "sub-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "listing vaults")
	})
}

// ---------------------------------------------------------------------------
// PurgeKeyVault — 404 branch (no vault to purge is a no-op)
// ---------------------------------------------------------------------------

func TestKeyVaultService_PurgeKeyVault(t *testing.T) {
	t.Parallel()

	t.Run("not found is treated as success", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/deletedVaults/") &&
				strings.Contains(req.URL.Path, "/purge")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		})

		svc := newTestService(mockHttp)
		err := svc.PurgeKeyVault(t.Context(), "sub-1", "gone", "eastus")
		require.NoError(t, err)
	})

	t.Run("forbidden surfaces error", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/deletedVaults/")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusForbidden)
		})

		svc := newTestService(mockHttp)
		err := svc.PurgeKeyVault(t.Context(), "sub-1", "nope", "eastus")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "starting purging key vault")
	})
}

// ---------------------------------------------------------------------------
// SecretFromAkvs / SecretFromKeyVaultReference (dispatch into data-plane)
// ---------------------------------------------------------------------------

func TestKeyVaultService_SecretFromAkvs(t *testing.T) {
	t.Parallel()

	t.Run("invalid reference returns error without HTTP", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		svc := newTestService(mockHttp)
		_, err := svc.SecretFromAkvs(t.Context(), "not-an-akvs")
		require.Error(t, err)
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/mysecret")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			body := map[string]any{
				"id":    "https://myvault.vault.azure.net/secrets/mysecret/abc",
				"value": "resolved",
			}
			return writeJSON(req, http.StatusOK, body), nil
		}))

		svc := newTestService(mockHttp)
		value, err := svc.SecretFromAkvs(t.Context(), "akvs://sub-1/myvault/mysecret")
		require.NoError(t, err)
		assert.Equal(t, "resolved", value)
	})

	t.Run("downstream error is wrapped", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		}))

		svc := newTestService(mockHttp)
		_, err := svc.SecretFromAkvs(t.Context(), "akvs://sub-1/myvault/missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetching secret value from key vault")
	})
}

func TestKeyVaultService_SecretFromKeyVaultReference(t *testing.T) {
	t.Parallel()

	t.Run("dispatches akvs to SecretFromAkvs", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/s")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			body := map[string]any{
				"id":    "https://myvault.vault.azure.net/secrets/s/v1",
				"value": "akvs-value",
			}
			return writeJSON(req, http.StatusOK, body), nil
		}))

		svc := newTestService(mockHttp)
		got, err := svc.SecretFromKeyVaultReference(t.Context(), "akvs://sub-1/myvault/s", "sub-1")
		require.NoError(t, err)
		assert.Equal(t, "akvs-value", got)
	})

	t.Run("app reference path fetches via vault URL", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/mysecret") &&
				strings.Contains(req.URL.Host, "myvault.vault.azure.net")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			body := map[string]any{
				"id":    "https://myvault.vault.azure.net/secrets/mysecret/xyz",
				"value": "app-ref-value",
			}
			return writeJSON(req, http.StatusOK, body), nil
		}))

		svc := newTestService(mockHttp)
		ref := "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret)"
		got, err := svc.SecretFromKeyVaultReference(t.Context(), ref, "sub-default")
		require.NoError(t, err)
		assert.Equal(t, "app-ref-value", got)
	})

	t.Run("app reference with version", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/mysecret/specific-version")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			body := map[string]any{
				"id":    "https://myvault.vault.azure.net/secrets/mysecret/specific-version",
				"value": "pinned-value",
			}
			return writeJSON(req, http.StatusOK, body), nil
		}))

		svc := newTestService(mockHttp)
		ref := "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret/specific-version)"
		got, err := svc.SecretFromKeyVaultReference(t.Context(), ref, "sub-default")
		require.NoError(t, err)
		assert.Equal(t, "pinned-value", got)
	})

	t.Run("app reference error is wrapped", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		mockHttp.When(func(req *http.Request) bool {
			return strings.Contains(req.URL.Path, "/secrets/")
		}).RespondFn(withChallengeAuth("https://vault.azure.net", func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		}))

		svc := newTestService(mockHttp)
		ref := "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/missing)"
		_, err := svc.SecretFromKeyVaultReference(t.Context(), ref, "sub-default")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetching secret")
	})

	t.Run("invalid app reference (bad host) errors before HTTP", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		svc := newTestService(mockHttp)
		ref := "@Microsoft.KeyVault(SecretUri=https://myvault.example.com/secrets/x)"
		_, err := svc.SecretFromKeyVaultReference(t.Context(), ref, "sub-default")
		require.Error(t, err)
	})

	t.Run("unrecognized format returns error", func(t *testing.T) {
		t.Parallel()
		mockHttp := mockhttp.NewMockHttpUtil()
		svc := newTestService(mockHttp)
		_, err := svc.SecretFromKeyVaultReference(t.Context(), "plain-value", "sub-default")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unrecognized Key Vault reference format")
	})
}

// ---------------------------------------------------------------------------
// Credential provider failure path — surfaces error from both client factories
// ---------------------------------------------------------------------------

// failingCredentialProvider implements account.SubscriptionCredentialProvider
// but always fails. This exercises the error branch in createKeyVaultClient
// and createSecretsDataClient without touching HTTP at all.
type failingCredentialProvider struct{}

func (failingCredentialProvider) CredentialForSubscription(
	_ context.Context, _ string,
) (azcore.TokenCredential, error) {
	return nil, errors.New("credential unavailable")
}

func TestKeyVaultService_CredentialProviderFailure(t *testing.T) {
	t.Parallel()

	// The ARM call path: GetKeyVault -> createKeyVaultClient -> credential error.
	mockHttp := mockhttp.NewMockHttpUtil()
	svc := newTestService(mockHttp)
	svc.credentialProvider = failingCredentialProvider{}

	_, err := svc.GetKeyVault(t.Context(), "sub", "rg", "v")
	require.Error(t, err)

	// The data-plane path: GetKeyVaultSecret -> createSecretsDataClient ->
	// credential error.
	_, err = svc.GetKeyVaultSecret(t.Context(), "sub", "v", "s")
	require.Error(t, err)

	// PurgeKeyVault goes through createKeyVaultClient as well.
	err = svc.PurgeKeyVault(t.Context(), "sub", "v", "eastus")
	require.Error(t, err)

	// ListSubscriptionVaults surfaces a wrapped error.
	_, err = svc.ListSubscriptionVaults(t.Context(), "sub")
	require.Error(t, err)

	// CreateVault surfaces a wrapped error.
	_, err = svc.CreateVault(t.Context(), "tenant", "sub", "rg", "eastus", "v")
	require.Error(t, err)
}
