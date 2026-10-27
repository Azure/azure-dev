// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type testEnvironment struct {
	values map[string]string
	names  []string
	fail   string
	name   string
}

func (e *testEnvironment) CurrentName(context.Context) (string, error) { return e.name, nil }

func (e *testEnvironment) Value(_ context.Context, name, key string) (string, error) {
	e.names = append(e.names, name)
	if key == e.fail {
		return "", errors.New("environment read failed")
	}
	return e.values[key], nil
}

func TestResolveEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name, selected, current, endpoint, skip, fail string
		wantTarget, wantError                         bool
	}{
		{name: "matching current environment", current: "default", wantTarget: true},
		{name: "selected environment", selected: "staging", current: "default", wantTarget: true},
		{name: "caller skip setting", current: "default", skip: "true"},
		{name: "no current environment"},
		{name: "different explicit endpoint", current: "default",
			endpoint: "https://other.services.ai.azure.com/api/projects/project"},
		{name: "read failure is not absence", current: "default", fail: "AZURE_AI_PROJECT_ID", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &testEnvironment{values: map[string]string{
				"AZURE_AI_PROJECT_ID": testProjectID, "FOUNDRY_PROJECT_ENDPOINT": testTarget.Endpoint, "SKIP_ACCESS": tt.skip,
			}, fail: tt.fail, name: tt.current}
			endpoint := tt.endpoint
			if endpoint == "" {
				endpoint = testTarget.Endpoint
			}
			target, err := ResolveEnvironment(t.Context(), environment, ContextOptions{
				Endpoint: endpoint, EnvironmentName: tt.selected, SkipKey: "SKIP_ACCESS",
			})
			if tt.wantError {
				require.Error(t, err)
				shared, ok := errors.AsType[*Error](err)
				require.True(t, ok)
				require.Equal(t, ResolutionFailed, shared.Code)
				return
			}
			require.NoError(t, err)
			if tt.wantTarget {
				require.Equal(t, &testTarget, target)
			} else {
				require.Nil(t, target)
			}
			for _, name := range environment.names {
				if tt.selected != "" {
					require.Equal(t, tt.selected, name)
				} else {
					require.Equal(t, tt.current, name)
				}
			}
		})
	}
}

func TestResolveEnvironmentCancellationAndFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := ResolveEnvironment(ctx, &testEnvironment{}, ContextOptions{})
	require.ErrorIs(t, err, context.Canceled)
	environment := &testEnvironment{name: "test", values: map[string]string{
		"AZURE_AI_PROJECT_ID": testProjectID, "AZURE_AI_PROJECT_ENDPOINT": testTarget.Endpoint,
	}}
	target, err := ResolveEnvironment(t.Context(), environment, ContextOptions{Endpoint: testTarget.Endpoint})
	require.NoError(t, err)
	require.Equal(t, &testTarget, target)
	_, err = ResolveEnvironment(t.Context(), nil, ContextOptions{})
	require.Error(t, err)
}
