// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azure.ai.toolboxes/internal/foundry/connections"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubConnectionDescriptorRunner struct {
	args   []string
	output []byte
	err    error
}

func (r *stubConnectionDescriptorRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.args = append([]string(nil), args...)
	return r.output, r.err
}

func TestDefaultConnectionResolverUsesConnectionExtension(t *testing.T) {
	t.Parallel()

	runner := &stubConnectionDescriptorRunner{output: []byte(`{
  "id": "/subscriptions/s/connections/github",
  "name": "github",
  "kind": "RemoteTool",
  "target": "https://api.githubcopilot.com/mcp/"
}`)}
	resolver := defaultConnectionResolver{runner: runner}

	connection, err := resolver.resolveConnection(
		t.Context(), "https://account.services.ai.azure.com/api/projects/project", "github",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ai", "connection", "show", "github",
		"--project-endpoint", "https://account.services.ai.azure.com/api/projects/project",
		"--output", "json", "--no-prompt",
	}, runner.args)
	assert.Equal(t, "/subscriptions/s/connections/github", connection.ID)
	assert.Equal(t, connections.ConnectionTypeRemoteTool, connection.Category)
	assert.Equal(t, "https://api.githubcopilot.com/mcp/", connection.Target)
}
