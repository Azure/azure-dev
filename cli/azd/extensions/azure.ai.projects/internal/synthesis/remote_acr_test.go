// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequiresRemoteACRBuild(t *testing.T) {
	for _, tt := range []struct {
		name, service string
		want          bool
	}{
		{"remote source", "kind: hosted\ndocker: {remoteBuild: true}", true},
		{"local source", "kind: hosted\ndocker: {remoteBuild: false}", false},
		{"core local default", "kind: hosted", false},
		{"prebuilt", "image: registry/image:v1\ndocker: {remoteBuild: true}", false},
		{"passthrough", "docker: {remoteBuild: true, imagePassthrough: true}", false},
		{"code", "codeConfiguration: {}\ndocker: {remoteBuild: true}", false},
		{"prompt", "kind: prompt\ndocker: {remoteBuild: true}", false},
		{"disabled", "condition: false\ndocker: {remoteBuild: true}", false},
		{"environment condition", "condition: ${BUILD_AGENT}\ndocker: {remoteBuild: true}", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			service := "host: azure.ai.agent\n" + tt.service + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte("host: azure.ai.agent\n"), 0o600))
			for _, ref := range []bool{false, true} {
				var body strings.Builder
				body.WriteString("services:\n  project:\n    host: azure.ai.project\n  agent:\n")
				if ref {
					body.WriteString("    $ref: agent.yaml\n")
					for line := range strings.SplitSeq(tt.service, "\n") {
						body.WriteString("    " + line + "\n")
					}
				} else {
					for line := range strings.SplitSeq(strings.TrimSuffix(service, "\n"), "\n") {
						body.WriteString("    " + line + "\n")
					}
				}
				got, err := RequiresRemoteACRBuild(Input{
					RawAzureYAML: []byte(body.String()), ProjectRoot: root,
					Env: map[string]string{"BUILD_AGENT": "true"},
				})
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
	for _, raw := range []string{
		"services:\n  project:\n    host: azure.ai.project\n",
		"services:\n  project:\n    host: azure.ai.project\n    agents:\n" +
			"      - kind: hosted\n        docker: {remoteBuild: false}\n",
	} {
		got, err := RequiresRemoteACRBuild(Input{RawAzureYAML: []byte(raw)})
		require.NoError(t, err)
		assert.False(t, got)
	}
	got, err := RequiresRemoteACRBuild(Input{RawAzureYAML: []byte(
		"services:\n  project:\n    host: azure.ai.project\n    agents:\n" +
			"      - kind: hosted\n        docker: {remoteBuild: true}\n")})
	require.NoError(t, err)
	assert.True(t, got)
}
