// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"maps"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

type fakeEnvironmentStore struct {
	name   string
	data   map[string]string
	writes []string
}

type fakeDeploymentSafetyChecker struct {
	err   error
	calls int
}

func (c *fakeDeploymentSafetyChecker) EnsureCanGenerate(
	context.Context,
	string,
	map[string]string,
) error {
	c.calls++
	return c.err
}

func (s *fakeEnvironmentStore) values(context.Context) (string, map[string]string, error) {
	values := make(map[string]string, len(s.data))
	maps.Copy(values, s.data)
	return s.name, values, nil
}

func (s *fakeEnvironmentStore) set(_ context.Context, _ string, key, value string) error {
	s.data[key] = value
	s.writes = append(s.writes, key)
	return nil
}

func clearProcessValue(t *testing.T, key string) {
	t.Helper()
	value, present := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestPreProvisionWritesModeAndPasswordForSameInvocation(t *testing.T) {
	clearProcessValue(t, databaseTypeEnv)
	clearProcessValue(t, databasePasswordEnv)
	project, service := createMavenProject(t)
	service.Config = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).Config
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{}}
	deployments := &fakeDeploymentSafetyChecker{}
	runtime := &Runtime{
		environments: store,
		deployments:  deployments,
		reconcile: func(context.Context, map[string]string) error {
			return nil
		},
	}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.NoError(t, err)
	require.Equal(t, "postgresql", store.data[databaseTypeEnv])
	require.NotEmpty(t, store.data[databasePasswordEnv])
	require.Equal(t, []string{databaseTypeEnv, databasePasswordEnv}, store.writes)
	require.Equal(t, 1, deployments.calls)
}

func TestPreProvisionValidatesFilesBeforeEnvironmentWrites(t *testing.T) {
	clearProcessValue(t, databasePasswordEnv)
	project, service := createMavenProject(t)
	service.Config = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).Config
	require.NoError(t, os.WriteFile(project.Path+string(os.PathSeparator)+"Dockerfile", []byte("FROM user"), 0o600))
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{}}
	runtime := &Runtime{environments: store}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "not owned by azure.springboot")
	require.Empty(t, store.writes)
	require.Empty(t, store.data)
}

func TestPreProvisionRejectsInvalidSuppliedPasswordsBeforeWritesOrAzureReads(t *testing.T) {
	project, service := createMavenProject(t)
	service.Config = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).Config

	for _, test := range []struct {
		name      string
		persisted string
		provided  string
		wantErr   string
	}{
		{
			name:     "explicit empty",
			provided: "",
			wantErr:  "explicitly empty",
		},
		{
			name:      "conflict",
			persisted: "Valid-Pass9!",
			provided:  "Other-Pass8!",
			wantErr:   "conflicts",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(databasePasswordEnv, test.provided)
			values := map[string]string{}
			if test.persisted != "" {
				values[databasePasswordEnv] = test.persisted
			}
			store := &fakeEnvironmentStore{name: "test", data: values}
			deployments := &fakeDeploymentSafetyChecker{}
			runtime := &Runtime{
				environments: store,
				deployments:  deployments,
			}

			err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
			require.ErrorContains(t, err, test.wantErr)
			require.Empty(t, store.writes)
			require.Zero(t, deployments.calls)
		})
	}
}

func TestPreProvisionFailsClosedBeforeWritesWhenDeploymentSafetyCannotBeEstablished(t *testing.T) {
	clearProcessValue(t, databasePasswordEnv)
	project, service := createMavenProject(t)
	service.Config = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).Config
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{}}
	deployments := &fakeDeploymentSafetyChecker{err: assertError("resource ownership read failed")}
	runtime := &Runtime{
		environments: store,
		deployments:  deployments,
	}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "resource ownership read failed")
	require.Empty(t, store.writes)
	require.Empty(t, store.data)
	require.Equal(t, 1, deployments.calls)
}

func TestPostProvisionPersistsModeBeforeFirewallFailure(t *testing.T) {
	clearProcessValue(t, databaseTypeEnv)
	project, service := createMavenProject(t)
	service.Config = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).Config
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{}}
	runtime := &Runtime{
		environments: store,
		reconcile: func(context.Context, map[string]string) error {
			return assertError("firewall denied")
		},
	}

	err := runtime.PostProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "firewall denied")
	require.Equal(t, "postgresql", store.data[provisionedDatabaseTypeEnv])
}

func TestPreProvisionRejectsInPlaceModeSwitch(t *testing.T) {
	project, service := createMavenProject(t)
	service.Config = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "h2"},
	}).Config
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{
		provisionedDatabaseTypeEnv: "postgresql",
	}}
	runtime := &Runtime{environments: store}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "provisioned for")
	require.Empty(t, store.writes)
}

type assertError string

func (e assertError) Error() string {
	return string(e)
}
