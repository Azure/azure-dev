// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// validateStubProjectServer serves a fixed ProjectConfig for Validate's provider
// and brownfield gates.
type validateStubProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	project *azdext.ProjectConfig
	err     error
}

func (s *validateStubProjectServer) Get(
	context.Context, *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.GetProjectResponse{Project: s.project}, nil
}

// validateStubEnvServer serves a fixed env name and keyed values for Validate's
// environment fallback.
type validateStubEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	envName string
	get     map[string]string
}

func (s *validateStubEnvServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: s.envName}}, nil
}

func (s *validateStubEnvServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Value: s.get[req.Key]}, nil
}

// newValidateTestClient spins up a gRPC server exposing the given project and
// environment stubs and returns an AzdClient connected to it.
func newValidateTestClient(
	t *testing.T,
	projSrv azdext.ProjectServiceServer,
	envSrv azdext.EnvironmentServiceServer,
) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterProjectServiceServer(srv, projSrv)
	azdext.RegisterEnvironmentServiceServer(srv, envSrv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(lis.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}

// writeAzureYAML writes an azure.yaml with a single Foundry project service to a
// fresh temp dir and returns the dir. When endpoint is non-empty the service is
// brownfield (bring-your-own).
func writeAzureYAML(t *testing.T, endpoint string) string {
	t.Helper()
	dir := t.TempDir()
	body := "name: rgloc-test\nservices:\n  ai-project:\n    host: " + FoundryProjectHost + "\n"
	if endpoint != "" {
		body += "    endpoint: " + endpoint + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte(body), 0o600))
	return dir
}

// provisionContext builds a "provision" ValidationContext from the given values.
// Empty values are omitted so the accessor reports (‑, false), exercising the
// environment fallback.
func provisionContext(subscriptionID, location, resourceGroup string) *azdext.ValidationContext {
	data := map[string][]byte{}
	if subscriptionID != "" {
		data[azdext.ValidationContextSubscriptionID] = []byte(subscriptionID)
	}
	if location != "" {
		data[azdext.ValidationContextEnvLocation] = []byte(location)
	}
	if resourceGroup != "" {
		data[azdext.ValidationContextResourceGroup] = []byte(resourceGroup)
	}
	return &azdext.ValidationContext{CheckType: azdext.ValidationCheckTypeProvision, Data: data}
}

func TestValidate_Gates(t *testing.T) {
	const sub = "00000000-0000-0000-0000-000000000000"

	t.Run("skips non-foundry provider without looking up the resource group", func(t *testing.T) {
		proj := &validateStubProjectServer{project: &azdext.ProjectConfig{
			Path:  writeAzureYAML(t, ""),
			Infra: &azdext.InfraOptions{Provider: "bicep"},
		}}
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{}}
		client := newValidateTestClient(t, proj, env)

		var called bool
		c := &ResourceGroupLocationCheck{azdClient: client}
		c.resourceGroupLocation = func(context.Context, string, string) (string, bool, error) {
			called = true
			return "eastus", true, nil
		}

		resp, err := c.Validate(t.Context(), provisionContext(sub, "westus2", "rg-x"), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.Results)
		assert.False(t, called, "resource group lookup must not run for a non-foundry provider")
	})

	t.Run("skips brownfield (endpoint) foundry project", func(t *testing.T) {
		proj := &validateStubProjectServer{project: &azdext.ProjectConfig{
			Path:  writeAzureYAML(t, "https://acct.services.ai.azure.com/api/projects/p"),
			Infra: &azdext.InfraOptions{Provider: FoundryProviderName},
		}}
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{}}
		client := newValidateTestClient(t, proj, env)

		var called bool
		c := &ResourceGroupLocationCheck{azdClient: client}
		c.resourceGroupLocation = func(context.Context, string, string) (string, bool, error) {
			called = true
			return "eastus", true, nil
		}

		resp, err := c.Validate(t.Context(), provisionContext(sub, "westus2", "rg-x"), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.Results)
		assert.False(t, called, "resource group lookup must not run for a brownfield project")
	})

	t.Run("skips when required values are missing", func(t *testing.T) {
		proj := &validateStubProjectServer{project: &azdext.ProjectConfig{
			Path:  writeAzureYAML(t, ""),
			Infra: &azdext.InfraOptions{Provider: FoundryProviderName},
		}}
		// Subscription present but no location, and none in the env: nothing to compare.
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{}}
		client := newValidateTestClient(t, proj, env)

		var called bool
		c := &ResourceGroupLocationCheck{azdClient: client}
		c.resourceGroupLocation = func(context.Context, string, string) (string, bool, error) {
			called = true
			return "eastus", true, nil
		}

		resp, err := c.Validate(t.Context(), provisionContext(sub, "", "rg-x"), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.Results)
		assert.False(t, called, "lookup must not run without both subscription and location")
	})
}

func TestValidate_GreenfieldFoundry(t *testing.T) {
	const sub = "00000000-0000-0000-0000-000000000000"

	newCheck := func(t *testing.T, env *validateStubEnvServer, lookup resourceGroupLocationLookup,
	) *ResourceGroupLocationCheck {
		proj := &validateStubProjectServer{project: &azdext.ProjectConfig{
			Path:  writeAzureYAML(t, ""),
			Infra: &azdext.InfraOptions{Provider: FoundryProviderName},
		}}
		client := newValidateTestClient(t, proj, env)
		c := &ResourceGroupLocationCheck{azdClient: client}
		c.resourceGroupLocation = lookup
		return c
	}

	t.Run("mismatched region produces a blocking error", func(t *testing.T) {
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{}}
		c := newCheck(t, env, func(context.Context, string, string) (string, bool, error) {
			return "eastus", true, nil
		})

		resp, err := c.Validate(t.Context(), provisionContext(sub, "westus2", "rg-x"), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Results, 1)
		assert.Equal(t,
			azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_ERROR, resp.Results[0].Severity)
		assert.Equal(t, ResourceGroupLocationRuleID, resp.Results[0].DiagnosticId)
	})

	t.Run("matching region produces no results", func(t *testing.T) {
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{}}
		c := newCheck(t, env, func(context.Context, string, string) (string, bool, error) {
			return "eastus", true, nil
		})

		resp, err := c.Validate(t.Context(), provisionContext(sub, "eastus", "rg-x"), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.Results)
	})

	t.Run("lookup failure is non-blocking", func(t *testing.T) {
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{}}
		c := newCheck(t, env, func(context.Context, string, string) (string, bool, error) {
			return "", false, errors.New("boom")
		})

		resp, err := c.Validate(t.Context(), provisionContext(sub, "westus2", "rg-x"), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.Results, "an error-severity result must not be produced when the lookup fails")
	})

	t.Run("resource group not found is non-blocking", func(t *testing.T) {
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{}}
		c := newCheck(t, env, func(context.Context, string, string) (string, bool, error) {
			return "", false, nil
		})

		resp, err := c.Validate(t.Context(), provisionContext(sub, "westus2", "rg-x"), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.Results)
	})

	t.Run("trims context values before comparing", func(t *testing.T) {
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{}}
		var gotRG string
		c := newCheck(t, env, func(_ context.Context, _, rg string) (string, bool, error) {
			gotRG = rg
			return "eastus", true, nil
		})

		// Whitespace-padded context values must be trimmed: location " eastus "
		// should match ARM's "eastus" (no false mismatch), and the resource group
		// must reach the lookup without surrounding spaces.
		resp, err := c.Validate(
			t.Context(), provisionContext("  "+sub+"  ", " eastus ", "  rg-x  "), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.Results)
		assert.Equal(t, "rg-x", gotRG)
	})

	t.Run("falls back to environment values when the context is empty", func(t *testing.T) {
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{
			"AZURE_SUBSCRIPTION_ID": sub,
			"AZURE_LOCATION":        "westus2",
			"AZURE_RESOURCE_GROUP":  "rg-from-env",
		}}
		var gotRG string
		c := newCheck(t, env, func(_ context.Context, _, rg string) (string, bool, error) {
			gotRG = rg
			return "eastus", true, nil
		})

		// Empty context => accessors report (‑, false) => the env fallback supplies
		// all three values.
		resp, err := c.Validate(t.Context(), provisionContext("", "", ""), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Results, 1)
		assert.Equal(t, "rg-from-env", gotRG)
	})

	t.Run("defaults the resource group name when none is provided", func(t *testing.T) {
		env := &validateStubEnvServer{envName: "rgloc-test", get: map[string]string{}}
		var gotRG string
		c := newCheck(t, env, func(_ context.Context, _, rg string) (string, bool, error) {
			gotRG = rg
			return "eastus", true, nil
		})

		resp, err := c.Validate(t.Context(), provisionContext(sub, "westus2", ""), &azdext.ValidationCheckRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Results, 1)
		assert.Equal(t, defaultResourceGroupName("rgloc-test"), gotRG)
	})
}
