// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinetuneClientUploadsLocalFileAsMultipartForm(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "training.jsonl")
	fileContent := "{\"input\":\"example\"}\n"
	if err := os.WriteFile(filePath, []byte(fileContent), 0o600); err != nil {
		t.Fatal(err)
	}

	credential := &testTokenCredential{}
	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", credential)
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", request.Method)
		}
		if request.URL.Path != finetuneFilesPath {
			t.Fatalf("expected path %q, got %q", finetuneFilesPath, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}

		mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil {
			t.Fatal(err)
		}
		if mediaType != "multipart/form-data" {
			t.Fatalf("expected multipart form, got %q", mediaType)
		}

		reader := multipart.NewReader(request.Body, parameters["boundary"])
		parts := map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			parts[part.FormName()] = string(data)
			if part.FormName() == "file" && part.FileName() != "training.jsonl" {
				t.Fatalf("expected uploaded file name %q, got %q", "training.jsonl", part.FileName())
			}
		}

		if parts["purpose"] != "fine-tune" {
			t.Fatalf("expected fine-tune purpose, got %q", parts["purpose"])
		}
		if parts["file"] != fileContent {
			t.Fatalf("expected file contents %q, got %q", fileContent, parts["file"])
		}

		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":"file-uploaded"}`)),
			Header:     make(http.Header),
		}, nil
	})

	resource, err := client.uploadFile(t.Context(), filePath)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Id != "file-uploaded" {
		t.Fatalf("expected uploaded file ID, got %q", resource.Id)
	}
	if len(credential.scopes) != 1 || credential.scopes[0] != finetuneTokenScope {
		t.Fatalf("expected fine-tuning token scope %q, got %v", finetuneTokenScope, credential.scopes)
	}
}
