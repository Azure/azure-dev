// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestLoadProjectServiceConfig(t *testing.T) {
	t.Parallel()

	deployment := synthesis.Deployment{
		Name: "gpt-4.1-mini",
		Model: synthesis.DeploymentModel{
			Format:  "OpenAI",
			Name:    "gpt-4.1-mini",
			Version: "2025-04-14",
		},
		Sku: synthesis.DeploymentSku{
			Name:     "GlobalStandard",
			Capacity: 10,
		},
	}
	props := mustProjectProperties(t, map[string]any{
		"endpoint": "https://example.services.ai.azure.com/" +
			"api/projects/example",
		"deployments": []any{
			map[string]any{
				"name": deployment.Name,
				"model": map[string]any{
					"format":  deployment.Model.Format,
					"name":    deployment.Model.Name,
					"version": deployment.Model.Version,
				},
				"sku": map[string]any{
					"name":     deployment.Sku.Name,
					"capacity": deployment.Sku.Capacity,
				},
			},
		},
	})

	tests := []struct {
		name     string
		service  *azdext.ServiceConfig
		wantSeen bool
	}{
		{
			name: "inline properties",
			service: &azdext.ServiceConfig{
				Host:                 aiProjectHost,
				AdditionalProperties: props,
			},
			wantSeen: true,
		},
		{
			name: "legacy config",
			service: &azdext.ServiceConfig{
				Host:   aiProjectHost,
				Config: props,
			},
			wantSeen: true,
		},
		{
			name: "unrelated host",
			service: &azdext.ServiceConfig{
				Host: "azure.ai.agent",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg, found, err := loadProjectServiceConfig(
				map[string]*azdext.ServiceConfig{
					"service": test.service,
				},
			)
			require.NoError(t, err)
			assert.Equal(t, test.wantSeen, found)
			if !found {
				return
			}
			require.Len(t, cfg.Deployments, 1)
			assert.Equal(t, deployment, cfg.Deployments[0])
		})
	}
}

func TestLoadProjectServiceConfigRejectsDuplicates(t *testing.T) {
	t.Parallel()

	services := map[string]*azdext.ServiceConfig{
		"zeta":  {Host: aiProjectHost},
		"alpha": {Host: aiProjectHost},
	}

	_, _, err := loadProjectServiceConfig(services)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alpha, zeta")
}

func TestProjectLifecycleHandlerWritesDeployments(t *testing.T) {
	t.Parallel()

	envServer := &recordingProjectEnvironmentServer{
		envName: "dev",
	}
	client := newProjectEnvironmentClient(t, envServer)
	props := mustProjectProperties(t, map[string]any{
		"deployments": []any{
			map[string]any{
				"name": "gpt-4.1-mini",
				"model": map[string]any{
					"format":  "OpenAI",
					"name":    "gpt-4.1-mini",
					"version": "2025-04-14",
				},
				"sku": map[string]any{
					"name":     "GlobalStandard",
					"capacity": 10,
				},
			},
		},
	})

	err := projectLifecycleHandler(
		t.Context(),
		client,
		&azdext.ProjectEventArgs{
			Project: &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{
					"project": {
						Host:                 aiProjectHost,
						AdditionalProperties: props,
					},
				},
			},
		},
	)
	require.NoError(t, err)

	envServer.mu.Lock()
	defer envServer.mu.Unlock()
	assert.Equal(t, "dev", envServer.envNameSet)
	assert.Equal(t, projectDeploymentsEnvKey, envServer.key)
	assert.Equal(
		t,
		`[{\"name\":\"gpt-4.1-mini\",`+
			`\"model\":{\"name\":\"gpt-4.1-mini\",`+
			`\"format\":\"OpenAI\",\"version\":\"2025-04-14\"},`+
			`\"sku\":{\"name\":\"GlobalStandard\",`+
			`\"capacity\":10}}]`,
		envServer.value,
	)
}

func TestProjectLifecycleHandlerClearsEmptyDeployments(t *testing.T) {
	t.Parallel()

	envServer := &recordingProjectEnvironmentServer{
		envName: "dev",
	}
	client := newProjectEnvironmentClient(t, envServer)

	err := projectLifecycleHandler(
		t.Context(),
		client,
		&azdext.ProjectEventArgs{
			Project: &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{
					"project": {Host: aiProjectHost},
				},
			},
		},
	)
	require.NoError(t, err)

	envServer.mu.Lock()
	defer envServer.mu.Unlock()
	assert.Equal(t, "[]", envServer.value)
}

func TestDeploymentEnvironmentContract(t *testing.T) {
	t.Parallel()

	value, err := encodeProjectDeployments([]synthesis.Deployment{
		{
			Name: `model"\name`,
			Model: synthesis.DeploymentModel{
				Name: `model"\name`,
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(
		t,
		`[{\"name\":\"model\\\"\\\\name\",\"model\":{\"name\":\"model\\\"\\\\name\",\"format\":\"\",\"version\":\"\"},\"sku\":{\"name\":\"\",\"capacity\":0}}]`,
		value,
	)

	//nolint:gosec // repository-controlled parity path
	source, err := os.ReadFile(filepath.Join(
		"../../../azure.ai.agents/internal/cmd/listen.go",
	))
	require.NoError(t, err)

	file, err := parser.ParseFile(
		token.NewFileSet(),
		"listen.go",
		source,
		0,
	)
	require.NoError(t, err)

	var found bool
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "deploymentEnvUpdate" {
			found = agentsDeploymentEnvWriterMatchesContract(
				function,
				projectDeploymentsEnvKey,
			)
			break
		}
	}
	assert.True(
		t,
		found,
		"agents deployment writer must keep the projects environment contract",
	)
}

func agentsDeploymentEnvWriterMatchesContract(
	function *ast.FuncDecl,
	key string,
) bool {
	var escapesBackslashes, escapesQuotes, writesKey bool
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		if selector, ok := call.Fun.(*ast.SelectorExpr); ok &&
			selector.Sel.Name == "ReplaceAll" && len(call.Args) == 3 {
			escapesBackslashes = escapesBackslashes ||
				isStringReplaceAll(call, "\\", "\\\\")
			escapesQuotes = escapesQuotes ||
				isStringReplaceAll(call, "\"", "\\\"")
			return true
		}

		if name, ok := call.Fun.(*ast.Ident); ok &&
			name.Name == "setEnvVar" && len(call.Args) == 5 {
			if literal, ok := call.Args[3].(*ast.BasicLit); ok {
				writesKey = literal.Value == strconv.Quote(key)
			}
		}
		return true
	})
	return escapesBackslashes && escapesQuotes && writesKey
}

func isStringReplaceAll(
	call *ast.CallExpr,
	old string,
	new string,
) bool {
	oldLiteral, oldOK := call.Args[1].(*ast.BasicLit)
	newLiteral, newOK := call.Args[2].(*ast.BasicLit)
	return oldOK && newOK &&
		oldLiteral.Value == strconv.Quote(old) &&
		newLiteral.Value == strconv.Quote(new)
}

func mustProjectProperties(
	t *testing.T,
	value map[string]any,
) *structpb.Struct {
	t.Helper()

	props, err := structpb.NewStruct(value)
	require.NoError(t, err)
	return props
}

type recordingProjectEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer

	mu         sync.Mutex
	envName    string
	envNameSet string
	key        string
	value      string
}

func (s *recordingProjectEnvironmentServer) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: s.envName},
	}, nil
}

func (s *recordingProjectEnvironmentServer) SetValue(
	_ context.Context,
	request *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.envNameSet = request.EnvName
	s.key = request.Key
	s.value = request.Value
	return &azdext.EmptyResponse{}, nil
}

func newProjectEnvironmentClient(
	t *testing.T,
	envServer azdext.EnvironmentServiceServer,
) *azdext.AzdClient {
	t.Helper()

	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, envServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})
	return client
}
