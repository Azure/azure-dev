// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"maps"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeEnvironmentStore struct {
	name   string
	data   map[string]string
	writes []string
}

type fakeDeveloperIdentityResolver struct {
	identity *developerIdentity
	err      error
	calls    int
}

type fakeDeploymentOutputVerifier struct {
	err   error
	calls int
	bound *deploymentBinding
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
