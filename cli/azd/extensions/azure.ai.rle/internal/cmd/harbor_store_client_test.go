// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestHarborBundleHashIsSha256Hex(t *testing.T) {
	hash := harborBundleHash([]byte("abc"))
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Fatalf("hash %q is not 64 lowercase hex", hash)
	}
	if hash == harborBundleHash([]byte("abd")) {
		t.Fatal("different bundles must hash differently")
	}
}

func newHarborTestClient(transport roundTripFunc) *rleClient {
	client := newRleClientWithCredential(
		"https://account.services.ai.azure.com/api/projects/project", &testTokenCredential{})
	client.httpClient.Transport = transport
	return client
}

func harborTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestUploadHarborBundleSkipsUploadWhenBlobExists(t *testing.T) {
	bundle := []byte("bundle")
	hash := harborBundleHash(bundle)
	client := newHarborTestClient(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/tasks/upload-url") {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		return harborTestResponse(http.StatusOK,
			`{"path":"task-data/`+hash+`.tar.gz","exists":true}`), nil
	})

	ref, err := client.uploadHarborBundle(t.Context(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Sha256 != hash || ref.Path != "task-data/"+hash+".tar.gz" {
		t.Fatalf("unexpected bundle ref %+v", ref)
	}
}

func TestUploadHarborBundlePutsToSasUrl(t *testing.T) {
	bundle := []byte("bundle")
	hash := harborBundleHash(bundle)
	var uploaded []byte
	client := newHarborTestClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "store.blob.core.windows.net" {
			if r.Method != http.MethodPut || r.Header.Get("x-ms-blob-type") != "BlockBlob" {
				t.Fatalf("unexpected storage request %s %v", r.Method, r.Header)
			}
			if r.Header.Get("Authorization") != "" {
				t.Fatal("storage upload must not carry the Foundry bearer token")
			}
			uploaded, _ = io.ReadAll(r.Body)
			return harborTestResponse(http.StatusCreated, ""), nil
		}
		return harborTestResponse(http.StatusOK,
			`{"path":"task-data/`+hash+`.tar.gz","exists":false,`+
				`"uploadUrl":"https://store.blob.core.windows.net/c/task-data/x?sig=s"}`), nil
	})

	if _, err := client.uploadHarborBundle(t.Context(), bundle); err != nil {
		t.Fatal(err)
	}
	if string(uploaded) != "bundle" {
		t.Fatalf("uploaded %q", uploaded)
	}
}

func TestUploadHarborBundleFailsOnStorageError(t *testing.T) {
	client := newHarborTestClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "store.blob.core.windows.net" {
			return harborTestResponse(http.StatusForbidden, ""), nil
		}
		return harborTestResponse(http.StatusOK,
			`{"path":"p","exists":false,"uploadUrl":"https://store.blob.core.windows.net/c/b?sig=s"}`), nil
	})
	if _, err := client.uploadHarborBundle(t.Context(), []byte("x")); err == nil {
		t.Fatal("expected storage error")
	}
}

func TestUploadHarborBundleDoesNotDiscloseURL(t *testing.T) {
	signedURL := &url.URL{
		Scheme:   "https",
		Host:     "store.blob.core.windows.net",
		Path:     "/c/b",
		RawQuery: url.Values{"sig": {"test-signature"}, "other": {"query-secret"}}.Encode(),
		Fragment: "fragment-secret",
	}
	userURL := *signedURL
	userURL.User = url.UserPassword("test-user", "test-password")
	insecureURL := *signedURL
	insecureURL.Scheme = "http"
	transportErr := errors.New("storage connection failed")
	for _, tc := range []struct {
		name       string
		uploadURL  string
		wantUpload bool
	}{
		{name: "transport failure", uploadURL: signedURL.String(), wantUpload: true},
		{name: "invalid URL", uploadURL: strings.Replace(signedURL.String(), "/c/b", "/%zz", 1)},
		{name: "user credentials", uploadURL: userURL.String()},
		{name: "insecure URL", uploadURL: insecureURL.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploaded := false
			client := newHarborTestClient(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/tasks/upload-url") {
					body, err := json.Marshal(harborUploadURLResponse{Path: "p", UploadURL: tc.uploadURL})
					if err != nil {
						t.Fatal(err)
					}
					return harborTestResponse(http.StatusOK, string(body)), nil
				}
				if r.URL.Host != signedURL.Host {
					t.Fatalf("unexpected request to %s", r.URL.Host)
				}
				uploaded = true
				if r.URL.Query().Get("sig") != "test-signature" {
					t.Fatal("upload must retain the signature")
				}
				return nil, transportErr
			})
			_, err := client.uploadHarborBundle(t.Context(), []byte("x"))
			if err == nil {
				t.Fatal("expected upload error")
			}
			if uploaded != tc.wantUpload {
				t.Fatalf("upload attempted = %v, want %v", uploaded, tc.wantUpload)
			}
			if tc.wantUpload && !errors.Is(err, transportErr) {
				t.Fatalf("expected original transport failure, got %v", err)
			}
			for _, secret := range []string{
				tc.uploadURL, "test-signature", "query-secret", "fragment-secret", "test-user", "test-password",
			} {
				if strings.Contains(err.Error(), secret) || strings.Contains(serviceError(err).Error(), secret) {
					t.Fatalf("upload error disclosed %q", secret)
				}
			}
		})
	}
}

func TestUploadHarborBundleRejectsRedirects(t *testing.T) {
	uploads := 0
	client := newHarborTestClient(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/tasks/upload-url") {
			return harborTestResponse(http.StatusOK,
				`{"path":"p","uploadUrl":"https://store.blob.core.windows.net/c/b?sig=s"}`), nil
		}
		if r.URL.Host != "store.blob.core.windows.net" {
			t.Fatalf("unexpected redirect or finalization request to %s", r.URL.Host)
		}
		uploads++
		response := harborTestResponse(http.StatusTemporaryRedirect, "")
		response.Header.Set("Location", "https://other.blob.core.windows.net/c/b?sig=redirect-secret")
		return response, nil
	})
	if _, err := client.uploadHarborBundle(t.Context(), []byte("x")); err == nil ||
		!strings.Contains(err.Error(), "307") {
		t.Fatalf("expected rejected redirect, got %v", err)
	}
	if uploads != 1 {
		t.Fatalf("expected one upload attempt, got %d", uploads)
	}
	if client.httpClient.CheckRedirect != nil {
		t.Fatal("bundle uploads must not change the shared client's redirect policy")
	}
}

func TestCreateRequestSerializesHarborTasks(t *testing.T) {
	request := v1EnvironmentRequest{Tasks: []harborTask{{
		Name: "t1", AcrImagePath: "r.azurecr.io/x:1",
		Bundle: harborTaskBundle{Path: "task-data/a.tar.gz", Sha256: "a"},
	}}}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tasks":[{"name":"t1","acrImagePath":"r.azurecr.io/x:1","bundle":{"path":"task-data/a.tar.gz","sha256":"a"}}]`) {
		t.Fatalf("unexpected JSON %s", data)
	}
}
