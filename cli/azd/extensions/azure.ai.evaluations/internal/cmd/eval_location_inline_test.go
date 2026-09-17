// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"testing"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// servingProjectServer answers Get with a project instead of a failure.
type servingProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	project *azdext.ProjectConfig
}

func (s *servingProjectServer) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: s.project}, nil
}

func clientServing(t *testing.T, proj *azdext.ProjectConfig) *azdext.AzdClient {
	t.Helper()

	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, &servingProjectServer{project: proj})

	listener, netErr := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, netErr)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

// An eval service may carry its configuration inline instead of pointing at a
// file, which is the shape schemas/examples/inline.azure.yaml documents and the
// one `azd up` deploys straight off the entry.
//
// There is no file to return for it. Answering with the default beneath the
// project root said "nothing is declared here" about a project that declares
// its evaluations in azure.yaml, so `create`, `generate` and `run` worked on a
// configuration the author never wrote while the deploy used the inline one.
func TestProjectEvalLocationRefusesAnInlineService(t *testing.T) {
	proj := projectWith("api")
	proj.Path = t.TempDir()
	proj.Services["support-agent-evals"] = &azdext.ServiceConfig{
		Name: "support-agent-evals",
		Host: project.EvalHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"datasets": []any{map[string]any{"name": "golden", "file": "./golden.jsonl"}},
			"evals":    []any{map[string]any{"name": "nightly"}},
		}),
	}

	got, err := projectEvalLocation(t.Context(), clientServing(t, proj))

	require.Error(t, err, "the default path is a different configuration, not this one")
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), "support-agent-evals", "the reader has to find it in azure.yaml")
	assert.Contains(t, err.Error(), "$ref", "and be told the shape these commands can read")
}

// The $ref form is unaffected, including when the same project also declares
// services that are not evals.
func TestProjectEvalLocationStillFollowsARef(t *testing.T) {
	root := t.TempDir()
	proj := projectWith("api")
	proj.Path = root
	proj.Services["support-agent-evals"] = &azdext.ServiceConfig{
		Name: "support-agent-evals",
		Host: project.EvalHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"$ref": "./evals/azure.eval.yaml",
		}),
	}

	got, err := projectEvalLocation(t.Context(), clientServing(t, proj))

	require.NoError(t, err)
	assert.Equal(t, project.UnderRoot(root, "evals/azure.eval.yaml"), got)
}

// A service declaring the eval host and nothing else is what `init` is about to
// write into, so it still resolves to the default rather than refusing.
func TestProjectEvalLocationDefaultsForAnEmptyService(t *testing.T) {
	root := t.TempDir()
	proj := projectWith("api")
	proj.Path = root
	proj.Services["evals"] = &azdext.ServiceConfig{Name: "evals", Host: project.EvalHost}

	got, err := projectEvalLocation(t.Context(), clientServing(t, proj))

	require.NoError(t, err)
	assert.Equal(t, project.UnderRoot(root, project.DefaultEvalDir), got)
}
