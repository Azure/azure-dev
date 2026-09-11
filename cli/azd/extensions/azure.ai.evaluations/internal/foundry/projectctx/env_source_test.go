// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeEnv answers the two reads with whatever the case under test needs.
type fakeEnv struct {
	current    *azdext.EnvironmentResponse
	currentErr error
	values     map[string]string
	valueErr   map[string]error
	asked      []string
}

func (f *fakeEnv) GetCurrent(
	context.Context, *azdext.EmptyRequest, ...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	return f.current, f.currentErr
}

func (f *fakeEnv) GetValue(
	_ context.Context, req *azdext.GetEnvRequest, _ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	f.asked = append(f.asked, req.Key)
	if err, ok := f.valueErr[req.Key]; ok {
		return nil, err
	}
	return &azdext.KeyValueResponse{Key: req.Key, Value: f.values[req.Key]}, nil
}

func envNamed(name string) *azdext.EnvironmentResponse {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: name}}
}

// An answer of "nothing here" leaves the cascade free to carry on; a failure to
// answer has to stop it, because carrying on resolves to a lower-priority
// endpoint that can belong to a different project.
//
// This is the rule that has regressed twice while every test passed, because
// the only seam was the whole function.
func TestReadEnvHostedSource_TellsAbsenceApartFromFailure(t *testing.T) {
	cases := []struct {
		name      string
		env       *fakeEnv
		wantValue string
		wantName  string
		wantErr   string
	}{
		{
			name: "no environment selected",
			env: &fakeEnv{currentErr: status.Error(codes.Unknown,
				"default environment not found")},
		},
		{
			name: "outside a project altogether",
			env: &fakeEnv{currentErr: status.Error(codes.Unknown,
				"no project exists; to create a new project, run `azd init`")},
		},
		{
			name: "the environment named in config is gone",
			env:  &fakeEnv{currentErr: status.Error(codes.Unknown, "'dev': environment not found")},
		},
		{
			name: "no daemon",
			env:  &fakeEnv{currentErr: status.Error(codes.Unavailable, "connection refused")},
		},
		{
			name:    "the login has expired",
			env:     &fakeEnv{currentErr: status.Error(codes.Unauthenticated, "expired")},
			wantErr: "expired",
		},
		{
			name: "the daemon broke while looking",
			env: &fakeEnv{currentErr: status.Error(codes.Unknown,
				"loading project state: permission denied")},
			wantErr: "loading project state",
		},
		{
			name:      "the foundry key answers",
			env:       &fakeEnv{current: envNamed("dev"), values: map[string]string{foundryEnvKey: "https://a"}},
			wantValue: "https://a",
			wantName:  "dev",
		},
		{
			// The key `azd ai agent init` and `azd add` persist, read only
			// when the newer one has nothing.
			name: "the older key answers when the newer one is empty",
			env: &fakeEnv{
				current: envNamed("dev"),
				values:  map[string]string{foundryEnvKey: "", azureAiEnvKey: "https://b"},
			},
			wantValue: "https://b",
			wantName:  "dev",
		},
		{
			name: "neither key is set",
			env:  &fakeEnv{current: envNamed("dev")},
		},
		{
			// A key that is simply absent must not stop the second one being
			// tried, nor the levels below.
			name: "the first key is absent",
			env: &fakeEnv{
				current:  envNamed("dev"),
				values:   map[string]string{azureAiEnvKey: "https://b"},
				valueErr: map[string]error{foundryEnvKey: status.Error(codes.NotFound, "no such key")},
			},
			wantValue: "https://b",
			wantName:  "dev",
		},
		{
			name: "reading a key failed",
			env: &fakeEnv{
				current:  envNamed("dev"),
				valueErr: map[string]error{foundryEnvKey: status.Error(codes.Internal, "boom")},
			},
			wantErr: "boom",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, name, err := readEnvHostedSource(context.Background(), tc.env)

			if tc.wantErr != "" {
				require.Error(t, err, "a failure to answer must stop the cascade")
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err, "an answer of nothing must let the cascade carry on")
			assert.Equal(t, tc.wantValue, value)
			assert.Equal(t, tc.wantName, name)
		})
	}
}

// The newer key wins, so a project carrying both does not silently prefer the
// one an older command wrote.
func TestReadEnvHostedSource_PrefersTheNewerKey(t *testing.T) {
	env := &fakeEnv{
		current: envNamed("dev"),
		values:  map[string]string{foundryEnvKey: "https://new", azureAiEnvKey: "https://old"},
	}

	value, _, err := readEnvHostedSource(context.Background(), env)

	require.NoError(t, err)
	assert.Equal(t, "https://new", value)
	assert.Equal(t, []string{foundryEnvKey}, env.asked,
		"the older key is not even read once the newer one answers")
}
