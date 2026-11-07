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
	err    error
	calls  int
}

type fakeDeveloperIdentityResolver struct {
	identity *developerIdentity
	err      error
	calls    int
}

type fakeDeploymentOutputVerifier struct {
	err       error
	calls     int
	readCalls int
	bound     *deploymentBinding
}

func (v *fakeDeploymentOutputVerifier) Read(context.Context, map[string]string) (*deploymentBinding, error) {
	v.readCalls++
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

func (s *fakeEnvironmentStore) values(context.Context) (string, map[string]string, error) {
	s.calls++
	if s.err != nil {
		return "", nil, s.err
	}
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

func TestProjectLifecycleSkipsProjectsWithoutSpringServices(t *testing.T) {
	store := &fakeEnvironmentStore{err: errors.New("environment must not be read")}
	outputs := &fakeDeploymentOutputVerifier{err: errors.New("deployment must not be read")}
	networkCalls := 0
	runtime := &Runtime{
		environments: store,
		outputs:      outputs,
		prepareNetwork: func(context.Context, *deploymentBinding) (func(context.Context) error, error) {
			networkCalls++
			return nil, errors.New("network must not be prepared")
		},
	}
	args := &azdext.ProjectEventArgs{Project: &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"worker": {Name: "worker", Language: "java", Host: "containerapp"},
		},
	}}

	require.NoError(t, runtime.PreProvision(t.Context(), args))
	require.NoError(t, runtime.PostProvision(t.Context(), args))
	require.Zero(t, store.calls)
	require.Zero(t, outputs.readCalls)
	require.Zero(t, outputs.calls)
	require.Zero(t, networkCalls)
}
