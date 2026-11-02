// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
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

type fakeDeveloperIdentityResolver struct {
	identity *developerIdentity
	err      error
	calls    int
}

type fakeUserAccessTenantResolver struct {
	tenant string
	err    error
	calls  int
}

func (r *fakeUserAccessTenantResolver) Resolve(context.Context, string) (string, error) {
	r.calls++
	return r.tenant, r.err
}

type fakeDeploymentOutputVerifier struct {
	err   error
	calls int
	bound *deploymentBinding
}

func TestResolveProjectRejectsMultipleSpringServices(t *testing.T) {
	project := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"api":    {Name: "api", Host: SpringHost},
		"worker": {Name: "worker", Host: SpringHost},
	}}

	_, _, err := (&Runtime{}).resolveProject(t.Context(), project, nil, false, false)

	require.Error(t, err)
	requireLocalErrorCode(t, err, "multiple_spring_services_unsupported")
}

func (v *fakeDeploymentOutputVerifier) Read(context.Context, map[string]string) (*deploymentBinding, error) {
	return v.bound, v.err
}

func (v *fakeDeploymentOutputVerifier) Verify(
	context.Context,
	resolvedDatabase,
	map[string]string,
) (*deploymentBinding, error) {
	v.calls++
	return nil, v.err
}

func (r *fakeDeveloperIdentityResolver) Resolve(
	context.Context,
	string,
	string,
) (*developerIdentity, error) {
	r.calls++
	return r.identity, r.err
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

type assertError string

func (e assertError) Error() string {
	return string(e)
}

func TestResolveServiceRejectsLegacyHost(t *testing.T) {
	runtime := &Runtime{}

	_, err := runtime.resolveService(t.Context(), nil, &azdext.ServiceConfig{
		Name: "petclinic",
		Host: "azure.springboot",
	}, nil, nil, false)
	require.ErrorContains(t, err, `service "petclinic" uses unsupported Spring host "azure.springboot"`)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, "unsupported_spring_host", localErr.Code)
	require.Equal(t, "set the service host to springbootapp", localErr.Suggestion)
}
