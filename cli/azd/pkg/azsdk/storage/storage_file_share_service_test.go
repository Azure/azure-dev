// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockSubscriptionCredentialProvider mocks account.SubscriptionCredentialProvider.
type mockSubscriptionCredentialProvider struct {
	mock.Mock
}

func (m *mockSubscriptionCredentialProvider) CredentialForSubscription(
	ctx context.Context, subscriptionId string,
) (azcore.TokenCredential, error) {
	args := m.Called(ctx, subscriptionId)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(azcore.TokenCredential), args.Error(1)
}

// fakeTokenCredential returns a static, valid-looking access token so the
// azfile SDK's bearer-token policy is satisfied. It performs no network IO.
type fakeTokenCredential struct{}

func (fakeTokenCredential) GetToken(
	ctx context.Context, opts policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "fake-token",
		ExpiresOn: time.Now().Add(1 * time.Hour),
	}, nil
}

// newFileShareTestServer spins up an in-process HTTPS server that returns
// "success" for every Azure Files REST call the SDK makes during UploadPath.
func newFileShareTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		httpDate := now.Format(http.TimeFormat)
		// Azure Files uses an ISO8601 format with 7-digit fractional seconds.
		fileTime := now.Format("2006-01-02T15:04:05.0000000Z")
		w.Header().Set("x-ms-request-id", "test-req-id")
		w.Header().Set("x-ms-version", "2023-11-03")
		w.Header().Set("x-ms-file-id", "123")
		w.Header().Set("x-ms-file-parent-id", "0")
		w.Header().Set("x-ms-file-attributes", "None")
		w.Header().Set("x-ms-file-creation-time", fileTime)
		w.Header().Set("x-ms-file-last-write-time", fileTime)
		w.Header().Set("x-ms-file-change-time", fileTime)
		w.Header().Set("x-ms-file-permission-key", "1:1")
		w.Header().Set("ETag", `"0x8D0000000000000"`)
		w.Header().Set("Last-Modified", httpDate)
		w.Header().Set("Date", httpDate)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// armOptionsForServer builds arm.ClientOptions that route HTTPS traffic to
// the httptest server (trusting its self-signed cert) and disable retries.
func armOptionsForServer(srv *httptest.Server) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: srv.Client(),
			Retry: policy.RetryOptions{
				MaxRetries: -1,
			},
		},
	}
}

func Test_NewFileShareService_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	creds := &mockSubscriptionCredentialProvider{}
	svc := NewFileShareService(creds, &arm.ClientOptions{})
	require.NotNil(t, svc)
	_, ok := svc.(*fileShareClient)
	require.True(t, ok, "expected *fileShareClient implementation")
}

func Test_FileShareService_UploadPath_CredentialError(t *testing.T) {
	t.Parallel()

	creds := &mockSubscriptionCredentialProvider{}
	creds.On("CredentialForSubscription", mock.Anything, "sub-id").
		Return(nil, errors.New("credential unavailable"))

	svc := NewFileShareService(creds, &arm.ClientOptions{})

	err := svc.UploadPath(t.Context(), "sub-id", "https://example.file.core.windows.net/share", "anything")
	require.Error(t, err)
	require.Contains(t, err.Error(), "credential unavailable")
	creds.AssertExpectations(t)
}

func Test_FileShareService_UploadPath_SourceNotFound(t *testing.T) {
	t.Parallel()

	creds := &mockSubscriptionCredentialProvider{}
	creds.On("CredentialForSubscription", mock.Anything, "sub-id").
		Return(fakeTokenCredential{}, nil)

	svc := NewFileShareService(creds, &arm.ClientOptions{})

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	err := svc.UploadPath(t.Context(), "sub-id", "https://example.file.core.windows.net/share", missing)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err) || strings.Contains(err.Error(), "does-not-exist"))
}

func Test_FileShareService_UploadPath_SingleFile(t *testing.T) {
	t.Parallel()

	srv := newFileShareTestServer(t)

	creds := &mockSubscriptionCredentialProvider{}
	creds.On("CredentialForSubscription", mock.Anything, "sub-id").
		Return(fakeTokenCredential{}, nil)

	svc := NewFileShareService(creds, armOptionsForServer(srv))

	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "upload.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("hello world"), 0o600))

	shareURL := srv.URL + "/share"
	err := svc.UploadPath(t.Context(), "sub-id", shareURL, srcFile)
	require.NoError(t, err)
	creds.AssertExpectations(t)
}

func Test_FileShareService_UploadPath_Directory(t *testing.T) {
	t.Parallel()

	srv := newFileShareTestServer(t)

	creds := &mockSubscriptionCredentialProvider{}
	creds.On("CredentialForSubscription", mock.Anything, "sub-id").
		Return(fakeTokenCredential{}, nil)

	svc := NewFileShareService(creds, armOptionsForServer(srv))

	tempDir := t.TempDir()
	// Nested directory with a couple of files to exercise directory walk and
	// the "create nested dir" code path inside uploadFile.
	nestedDir := filepath.Join(tempDir, "sub", "deep")
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "root.txt"), []byte("root"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(nestedDir, "nested.txt"), []byte("nested"), 0o600))

	shareURL := srv.URL + "/share"
	err := svc.UploadPath(t.Context(), "sub-id", shareURL, tempDir)
	require.NoError(t, err)
	creds.AssertExpectations(t)
}

func Test_FileShareService_UploadPath_ServerError(t *testing.T) {
	t.Parallel()

	// Server that always fails — exercises the error path through uploadFile.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-ms-error-code", "AuthenticationFailed")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	creds := &mockSubscriptionCredentialProvider{}
	creds.On("CredentialForSubscription", mock.Anything, "sub-id").
		Return(fakeTokenCredential{}, nil)

	svc := NewFileShareService(creds, armOptionsForServer(srv))

	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "upload.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("data"), 0o600))

	err := svc.UploadPath(t.Context(), "sub-id", srv.URL+"/share", srcFile)
	require.Error(t, err)
	require.Contains(t, err.Error(), "uploading single file to file share")
}

func Test_FileShareService_UploadPath_Directory_ServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-ms-error-code", "AuthenticationFailed")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	creds := &mockSubscriptionCredentialProvider{}
	creds.On("CredentialForSubscription", mock.Anything, "sub-id").
		Return(fakeTokenCredential{}, nil)

	svc := NewFileShareService(creds, armOptionsForServer(srv))

	tempDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "root.txt"), []byte("root"), 0o600))

	err := svc.UploadPath(t.Context(), "sub-id", srv.URL+"/share", tempDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "uploading folder to file share")
}

// Test_FileShareService_UploadPath_DirectoryAlreadyExists verifies that the
// "directory already exists" error (returned by Azure Files when creating a
// directory that's already present) is treated as success — exercising the
// `strings.Contains(err.Error(), "ResourceAlreadyExists")` branch.
func Test_FileShareService_UploadPath_DirectoryAlreadyExists(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		httpDate := now.Format(http.TimeFormat)
		fileTime := now.Format("2006-01-02T15:04:05.0000000Z")

		w.Header().Set("x-ms-request-id", "test-req-id")
		w.Header().Set("x-ms-version", "2023-11-03")
		w.Header().Set("Date", httpDate)

		// Directory create (restype=directory) -> 409 ResourceAlreadyExists
		if strings.Contains(r.URL.RawQuery, "restype=directory") {
			w.Header().Set("x-ms-error-code", "ResourceAlreadyExists")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(
				`<?xml version="1.0" encoding="utf-8"?><Error>` +
					`<Code>ResourceAlreadyExists</Code>` +
					`<Message>The specified resource already exists.</Message>` +
					`</Error>`))
			return
		}

		// File create / upload -> success.
		w.Header().Set("x-ms-file-id", "123")
		w.Header().Set("x-ms-file-parent-id", "0")
		w.Header().Set("x-ms-file-attributes", "None")
		w.Header().Set("x-ms-file-creation-time", fileTime)
		w.Header().Set("x-ms-file-last-write-time", fileTime)
		w.Header().Set("x-ms-file-change-time", fileTime)
		w.Header().Set("x-ms-file-permission-key", "1:1")
		w.Header().Set("ETag", `"0x8D0000000000000"`)
		w.Header().Set("Last-Modified", httpDate)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	creds := &mockSubscriptionCredentialProvider{}
	creds.On("CredentialForSubscription", mock.Anything, "sub-id").
		Return(fakeTokenCredential{}, nil)

	svc := NewFileShareService(creds, armOptionsForServer(srv))

	tempDir := t.TempDir()
	nested := filepath.Join(tempDir, "already-exists")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "file.txt"), []byte("x"), 0o600))

	err := svc.UploadPath(t.Context(), "sub-id", srv.URL+"/share", tempDir)
	require.NoError(t, err)
}
