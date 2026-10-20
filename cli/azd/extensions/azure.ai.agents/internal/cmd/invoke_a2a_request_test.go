// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestA2ATextRequestBuilders(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		version string
		build   a2aTextRequestBuilder
	}{
		{"0.3", buildA2A03TextRequest},
		{"1.0", buildA2A10TextRequest},
	} {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()
			request := tt.build("Hello!", "hello-1", "hello-message-1")
			body, err := json.Marshal(request)
			require.NoError(t, err)
			golden, err := os.ReadFile(filepath.Join("testdata", "a2a", "request-"+tt.version+".json"))
			require.NoError(t, err)
			require.JSONEq(t, string(golden), string(body))

			// The selector must select this exact dialect, not a default body
			// subsequently patched by version-dependent branches.
			selected, err := a2aRequestBuilderForVersion(tt.version)
			require.NoError(t, err)
			selectedBody, err := json.Marshal(selected("Hello!", "hello-1", "hello-message-1"))
			require.NoError(t, err)
			require.JSONEq(t, string(golden), string(selectedBody))
		})
	}
}

func TestA2ARequestBuilderRejectsUnknownVersion(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"", "latest", "1.1", "2.0", "1.0.0", " 1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			build, err := a2aRequestBuilderForVersion(version)
			require.ErrorContains(t, err, "supported versions are 0.3 and 1.0")
			require.Nil(t, build)
			require.EqualError(t, validateA2AVersion(version), err.Error())
		})
	}
}

func TestA2ATextRequestBuildersPreserveText(t *testing.T) {
	t.Parallel()
	const text = "A quote: \"hello\"\nA backslash: \\ and Unicode: \u4f60\u597d"
	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			build, err := a2aRequestBuilderForVersion(version)
			require.NoError(t, err)
			body, err := json.Marshal(build(text, "request-id", "message-id"))
			require.NoError(t, err)
			var request struct {
				ID     string `json:"id"`
				Params struct {
					Message struct {
						MessageID string `json:"messageId"`
						Parts     []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"message"`
				} `json:"params"`
			}
			require.NoError(t, json.Unmarshal(body, &request))
			require.Equal(t, "request-id", request.ID)
			require.Equal(t, "message-id", request.Params.Message.MessageID)
			require.Len(t, request.Params.Message.Parts, 1)
			require.Equal(t, text, request.Params.Message.Parts[0].Text)
		})
	}
}

func TestA2AReadmeRequestsMatchWireFixtures(t *testing.T) {
	t.Parallel()
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)
	lfReadme := strings.ReplaceAll(string(readme), "\r\n", "\n")
	for _, tt := range []struct{ name, text string }{
		{"LF", lfReadme},
		{"CRLF", strings.ReplaceAll(lfReadme, "\n", "\r\n")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Windows checkouts may use CRLF; markdown and JSON semantics do not depend on it.
			text := strings.ReplaceAll(tt.text, "\r\n", "\n")
			_, section, found := strings.Cut(text, "## Invoking agents over A2A\n")
			require.True(t, found)
			section, _, _ = strings.Cut(section, "\n## ")
			examples := regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindAllStringSubmatch(section, -1)
			seen := map[string]bool{}
			for _, example := range examples {
				var request struct {
					Method string `json:"method"`
				}
				require.NoError(t, json.Unmarshal([]byte(example[1]), &request))
				var version string
				switch request.Method {
				case "message/send":
					version = "0.3"
				case "SendMessage":
					version = "1.0"
				default:
					continue
				}
				golden, err := os.ReadFile(filepath.Join("testdata", "a2a", "request-"+version+".json"))
				require.NoError(t, err)
				require.JSONEq(t, string(golden), example[1], "README A2A %s request must remain usable", version)
				seen[version] = true
			}
			require.Equal(t, map[string]bool{"0.3": true, "1.0": true}, seen)
		})
	}
}
