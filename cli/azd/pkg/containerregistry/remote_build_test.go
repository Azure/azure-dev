// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/require"
)

const (
	testSubscriptionID = "00000000-0000-0000-0000-000000000000"
	testResourceGroup  = "test-rg"
	testRegistryName   = "testregistry"
)

// armOptionsNoRetry returns arm.ClientOptions that route traffic through the given transport
// and disable retries so error-path tests fail fast.
func armOptionsNoRetry(transport policy.Transporter) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: transport,
			Retry: policy.RetryOptions{
				MaxRetries: -1,
			},
		},
	}
}

func TestUploadBuildSource_CredentialError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("credential failure")
	credProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return nil, expectedErr
		},
	)

	mgr := NewRemoteBuildManager(credProvider, &arm.ClientOptions{})

	_, err := mgr.UploadBuildSource(
		t.Context(), testSubscriptionID, testResourceGroup, testRegistryName, "does-not-matter",
	)
	require.ErrorIs(t, err, expectedErr)
}

func TestUploadBuildSource_GetBuildSourceUploadURLError(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return strings.Contains(request.URL.Path, "listBuildSourceUploadUrl")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusBadRequest, map[string]any{
				"error": map[string]string{
					"code":    "BadRequest",
					"message": "registry not found",
				},
			},
		)
	})

	mgr := NewRemoteBuildManager(
		mockCtx.SubscriptionCredentialProvider,
		armOptionsNoRetry(mockCtx.HttpClient),
	)

	_, err := mgr.UploadBuildSource(
		t.Context(), testSubscriptionID, testResourceGroup, testRegistryName, "irrelevant",
	)
	require.Error(t, err)
}

func TestUploadBuildSource_OpenFileError(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	// respond to upload URL request with a valid URL so we proceed to os.Open
	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return strings.Contains(request.URL.Path, "listBuildSourceUploadUrl")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		uploadURL := "https://upload.example.com/container/source.tar.gz?sig=abc"
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, armcontainerregistry.SourceUploadDefinition{
				UploadURL:    &uploadURL,
				RelativePath: strPtr("source.tar.gz"),
			},
		)
	})

	mgr := NewRemoteBuildManager(
		mockCtx.SubscriptionCredentialProvider,
		armOptionsNoRetry(mockCtx.HttpClient),
	)

	nonExistent := filepath.Join(t.TempDir(), "does-not-exist.tar.gz")
	_, err := mgr.UploadBuildSource(
		t.Context(), testSubscriptionID, testResourceGroup, testRegistryName, nonExistent,
	)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") ||
		strings.Contains(err.Error(), "cannot find"),
		"expected file-not-found error, got: %v", err)
}

func TestUploadBuildSource_Success(t *testing.T) {
	t.Parallel()

	// Spin up an httptest server that plays the role of both the ARM endpoint and the
	// blob upload target. The azure-sdk-for-go blob client issues PUT requests to the
	// upload URL returned by listBuildSourceUploadUrl.
	var uploadedBody atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "listBuildSourceUploadUrl"):
			// ARM call — respond with an upload URL pointing back at this server.
			host := r.Host
			uploadURL := "https://" + host + "/upload-container/source.tar.gz?sig=abc"
			def := armcontainerregistry.SourceUploadDefinition{
				UploadURL:    &uploadURL,
				RelativePath: strPtr("source.tar.gz"),
			}
			body, err := json.Marshal(def)
			require.NoError(t, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		case strings.Contains(r.URL.Path, "/upload-container/"):
			// Blob upload path — must be PUT of BlockBlob. Assert shape so
			// regressions that swap the verb/header/URL-shape are caught.
			require.Equal(t, http.MethodPut, r.Method,
				"expected PUT to blob upload URL, got %s", r.Method)
			require.Contains(t, r.URL.RawQuery, "sig=abc",
				"expected SAS query string to be preserved on upload")
			b, _ := io.ReadAll(r.Body)
			uploadedBody.Add(int64(len(b)))
			w.Header().Set("ETag", "\"0x8D4BCC2E4835CD0\"")
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	// Point the ARM SDK at our test server by overriding the host in its Transport.
	// The default ARM endpoint is management.azure.com; the SDK will issue requests to
	// that host, so we rewrite the target URL on its way out.
	transport := &rewritingTransport{
		inner: srv.Client(),
		host:  mustParseURL(srv.URL).Host,
	}

	armOpts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: transport,
			Retry: policy.RetryOptions{
				MaxRetries: -1,
			},
		},
	}

	credProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return &mocks.MockCredentials{}, nil
		},
	)

	mgr := NewRemoteBuildManager(credProvider, armOpts)

	// Create a small file to upload.
	srcPath := filepath.Join(t.TempDir(), "context.tar.gz")
	require.NoError(t, os.WriteFile(srcPath, []byte("fake-tarball-content"), 0600))

	res, err := mgr.UploadBuildSource(
		t.Context(), testSubscriptionID, testResourceGroup, testRegistryName, srcPath,
	)
	require.NoError(t, err)
	require.NotNil(t, res.UploadURL)
	require.NotNil(t, res.RelativePath)
	require.Equal(t, "source.tar.gz", *res.RelativePath)
	// Exact byte count — not just > 0. Catches regressions that would upload a
	// different (e.g. truncated) body.
	require.Equal(t, int64(len("fake-tarball-content")), uploadedBody.Load(),
		"expected blob upload to receive the full file content")
}

func TestRunDockerBuildRequestWithLogs_CredentialError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("credential failure")
	credProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return nil, expectedErr
		},
	)

	mgr := NewRemoteBuildManager(credProvider, &arm.ClientOptions{})

	err := mgr.RunDockerBuildRequestWithLogs(
		t.Context(), testSubscriptionID, testResourceGroup, testRegistryName,
		&armcontainerregistry.DockerBuildRequest{}, io.Discard,
	)
	require.ErrorIs(t, err, expectedErr)
}

func TestRunDockerBuildRequestWithLogs_ScheduleRunError(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return strings.Contains(request.URL.Path, "scheduleRun")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusBadRequest, map[string]any{
				"error": map[string]string{
					"code":    "BadRequest",
					"message": "invalid build request",
				},
			},
		)
	})

	mgr := NewRemoteBuildManager(
		mockCtx.SubscriptionCredentialProvider,
		armOptionsNoRetry(mockCtx.HttpClient),
	)

	err := mgr.RunDockerBuildRequestWithLogs(
		t.Context(), testSubscriptionID, testResourceGroup, testRegistryName,
		&armcontainerregistry.DockerBuildRequest{}, io.Discard,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "BadRequest")
}

func TestTerminalContainerRegistryRunStates(t *testing.T) {
	t.Parallel()

	// Guard against regressions: these are the statuses considered terminal.
	expected := []armcontainerregistry.RunStatus{
		armcontainerregistry.RunStatusCanceled,
		armcontainerregistry.RunStatusError,
		armcontainerregistry.RunStatusFailed,
		armcontainerregistry.RunStatusSucceeded,
		armcontainerregistry.RunStatusTimeout,
	}
	require.ElementsMatch(t, expected, terminalContainerRegistryRunStates)

	// Intermediate/unknown statuses must NOT be terminal — otherwise the
	// RunDockerBuildRequestWithLogs retry loop would exit prematurely.
	nonTerminal := []armcontainerregistry.RunStatus{
		armcontainerregistry.RunStatusQueued,
		armcontainerregistry.RunStatusRunning,
		armcontainerregistry.RunStatusStarted,
	}
	for _, s := range nonTerminal {
		require.NotContains(t, terminalContainerRegistryRunStates, s,
			"status %q must not be considered terminal", s)
	}
}

// rewritingTransport rewrites outbound request URLs to point at the configured host while
// preserving the path/query. It lets ARM SDK calls targeting management.azure.com land on a
// local httptest server without bypassing the SDK's client-side plumbing.
type rewritingTransport struct {
	inner *http.Client
	host  string
}

func (r *rewritingTransport) Do(req *http.Request) (*http.Response, error) {
	newURL := *req.URL
	newURL.Scheme = "https"
	newURL.Host = r.host
	clone := req.Clone(req.Context())
	clone.URL = &newURL
	clone.Host = r.host
	// RequestURI must be empty for client requests.
	clone.RequestURI = ""
	return r.inner.Do(clone)
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(fmt.Sprintf("invalid url %q: %v", raw, err))
	}
	return u
}

func strPtr(s string) *string { return &s }
