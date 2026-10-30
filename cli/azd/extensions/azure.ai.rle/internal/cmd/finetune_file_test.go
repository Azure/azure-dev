// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
			if errors.Is(err, io.EOF) {
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
			Body:       io.NopCloser(strings.NewReader(`{"id":"file-uploaded","status":"processed"}`)),
			Header:     make(http.Header),
		}, nil
	})

	resource, err := client.uploadFile(t.Context(), filePath)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Id != "file-uploaded" || resource.Status != "processed" {
		t.Fatalf("expected processed uploaded file, got %#v", resource)
	}
	if len(credential.scopes) != 1 || credential.scopes[0] != finetuneTokenScope {
		t.Fatalf("expected fine-tuning token scope %q, got %v", finetuneTokenScope, credential.scopes)
	}
}

func TestFinetuneClientDoesNotPollProcessedFile(t *testing.T) {
	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("processed file should not be polled: %s %s", request.Method, request.URL.Path)
		return nil, nil
	})

	err := client.waitForFileProcessed(
		t.Context(),
		&finetuneFileResource{Id: "file-uploaded", Status: "processed"},
		time.Second,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestFinetuneClientWaitsForFileToBeProcessed(t *testing.T) {
	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})
	getCalls := 0
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != finetuneFilesPath+"/file-uploaded" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		getCalls++
		status := "running"
		if getCalls == 2 {
			status = "processed"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"file-uploaded","status":"` + status + `"}`)),
			Header:     make(http.Header),
		}, nil
	})

	err := client.waitForFileProcessed(
		t.Context(),
		&finetuneFileResource{Id: "file-uploaded", Status: "pending"},
		time.Second,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if getCalls != 2 {
		t.Fatalf("expected two status polls, got %d", getCalls)
	}
}

func TestFinetuneClientStopsWaitingForTerminalFileStatus(t *testing.T) {
	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"id":"file-uploaded","status":"error","status_details":"invalid JSONL"}`)),
			Header: make(http.Header),
		}, nil
	})

	err := client.waitForFileProcessed(
		t.Context(),
		&finetuneFileResource{Id: "file-uploaded", Status: "pending"},
		time.Second,
		0,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid JSONL") || !strings.Contains(err.Error(), "error") {
		t.Fatalf("expected terminal import error, got %v", err)
	}
}

func TestFinetuneClientFileImportWaitTimesOut(t *testing.T) {
	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})

	err := client.waitForFileProcessed(
		t.Context(),
		&finetuneFileResource{Id: "file-uploaded", Status: "pending"},
		0,
		0,
	)
	if err == nil || !strings.Contains(err.Error(), "did not reach processed") {
		t.Fatalf("expected import timeout, got %v", err)
	}
}

func TestFinetuneClientFileImportWaitHonorsContextCancellation(t *testing.T) {
	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := client.waitForFileProcessed(
		ctx,
		&finetuneFileResource{Id: "file-uploaded", Status: "pending"},
		time.Second,
		time.Hour,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
