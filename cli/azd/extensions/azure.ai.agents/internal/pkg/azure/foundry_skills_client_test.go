// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestSkillsClient(endpoint string, fn roundTripFunc) *FoundrySkillsClient {
	return &FoundrySkillsClient{
		endpoint: endpoint,
		pipeline: newTestPipeline(fn),
	}
}

func TestCreateSkillVersion_RequestShape(t *testing.T) {
	var captured *http.Request
	var body []byte

	client := newTestSkillsClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		captured = req
		if req.Body != nil {
			body, _ = io.ReadAll(req.Body)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":"s-1","name":"my-skill","version":"1.2.0"}`)),
			Header:     make(http.Header),
		}, nil
	})

	out, err := client.CreateSkillVersion(t.Context(), "my skill", &CreateSkillVersionRequest{
		InlineContent: SkillInlineContent{
			Description:  "does things",
			Instructions: "You are a helpful skill.",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "1.2.0", out.Version)

	require.NotNil(t, captured)
	require.Equal(t, http.MethodPost, captured.Method)
	require.Equal(t, "/skills/my%20skill/versions", captured.URL.EscapedPath())
	require.Equal(t, "api-version="+skillsApiVersion, captured.URL.RawQuery)
	require.Equal(t, skillsFeatureHeader, captured.Header.Get("Foundry-Features"))
	// inline_content is an object with description + instructions (no envelope).
	require.Contains(t, string(body), `"inline_content"`)
	require.Contains(t, string(body), `"instructions":"You are a helpful skill."`)
	require.Contains(t, string(body), `"description":"does things"`)
}

func TestCreateSkillVersion_ErrorStatus(t *testing.T) {
	client := newTestSkillsClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad"}`)),
			Header:     make(http.Header),
		}, nil
	})
	_, err := client.CreateSkillVersion(t.Context(), "s", &CreateSkillVersionRequest{
		InlineContent: SkillInlineContent{Instructions: "x"},
	})
	require.Error(t, err)
}

func TestCreateSkillVersionFromFiles_UploadsEveryFile(t *testing.T) {
	var captured *http.Request
	var body []byte

	client := newTestSkillsClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		captured = req
		if req.Body != nil {
			body, _ = io.ReadAll(req.Body)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":"s-1","name":"my-skill","version":"1.0.0"}`)),
			Header:     make(http.Header),
		}, nil
	})

	files := map[string][]byte{
		"SKILL.md":            []byte("---\nname: my-skill\n---\nbody"),
		"references/tone.md":  []byte("tone guidance"),
		"assets/logo.svg":     []byte("<svg/>"),
		"scripts/analysis.py": []byte("print('hi')"),
	}

	out, err := client.CreateSkillVersionFromFiles(t.Context(), "my-skill", files)
	require.NoError(t, err)
	require.Equal(t, "1.0.0", out.Version)

	require.NotNil(t, captured)
	require.Equal(t, http.MethodPost, captured.Method)
	require.Equal(t, "/skills/my-skill/versions", captured.URL.EscapedPath())
	require.Contains(t, captured.Header.Get("Content-Type"), "multipart/form-data")
	require.Equal(t, skillsFeatureHeader, captured.Header.Get("Foundry-Features"))

	// Every file in the bundle — not just SKILL.md — must be present in the
	// multipart body. This is the regression this test guards: uploading only
	// SKILL.md silently drops references/, assets/, and any other bundle files.
	bodyStr := string(body)
	for name, content := range files {
		require.Contains(t, bodyStr, name, "multipart body missing file part for %q", name)
		require.Contains(t, bodyStr, string(content), "multipart body missing content for %q", name)
	}
}

func TestCreateSkillVersionFromFiles_EmptyFilesErrors(t *testing.T) {
	client := newTestSkillsClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		t.Fatal("no HTTP request should be made when files is empty")
		return nil, nil
	})
	_, err := client.CreateSkillVersionFromFiles(t.Context(), "s", map[string][]byte{})
	require.Error(t, err)
}

func TestCreateSkillVersionFromFiles_ErrorStatus(t *testing.T) {
	client := newTestSkillsClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad"}`)),
			Header:     make(http.Header),
		}, nil
	})
	_, err := client.CreateSkillVersionFromFiles(t.Context(), "s", map[string][]byte{"SKILL.md": []byte("x")})
	require.Error(t, err)
}

func TestPromoteSkillVersion_RequestShape(t *testing.T) {
	var captured *http.Request
	var body []byte

	client := newTestSkillsClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		captured = req
		if req.Body != nil {
			body, _ = io.ReadAll(req.Body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"s-1","name":"my-skill","default_version":"1.2.0"}`)),
			Header:     make(http.Header),
		}, nil
	})

	err := client.PromoteSkillVersion(t.Context(), "my-skill", "1.2.0")
	require.NoError(t, err)

	require.NotNil(t, captured)
	require.Equal(t, http.MethodPost, captured.Method)
	require.Equal(t, "/skills/my-skill", captured.URL.EscapedPath())
	require.Equal(t, "api-version="+skillsApiVersion, captured.URL.RawQuery)
	require.Equal(t, skillsFeatureHeader, captured.Header.Get("Foundry-Features"))
	require.Contains(t, string(body), `"default_version":"1.2.0"`)
}

func TestPromoteSkillVersion_ErrorStatus(t *testing.T) {
	client := newTestSkillsClient("https://proj.example.com", func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad"}`)),
			Header:     make(http.Header),
		}, nil
	})
	err := client.PromoteSkillVersion(t.Context(), "s", "1.0.0")
	require.Error(t, err)
}
