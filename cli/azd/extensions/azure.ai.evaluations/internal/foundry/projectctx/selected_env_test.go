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
)

// -e/--environment is parsed by the SDK and then has to be acted on. It was
// discarded, so `azd ai eval create -e staging` read its endpoint out of the
// default environment and wrote its ids back there -- and `-e a-name-azd-
// rejects` was accepted in silence, because nothing ever asked azd about it.
//
// These tests pin that the named environment is the one read, and that azd is
// not asked which environment is current when a name was given: asking can only
// produce a second, disagreeing answer.

// perEnv answers GetValue per environment, which is what tells "read staging"
// apart from "read whatever azd calls current".
type perEnv struct {
	values       map[string]map[string]string
	current      string
	currentCalls int
	askedEnvs    []string
}

func (f *perEnv) GetCurrent(
	context.Context, *azdext.EmptyRequest, ...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	f.currentCalls++
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: f.current},
	}, nil
}

func (f *perEnv) GetValue(
	_ context.Context, req *azdext.GetEnvRequest, _ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	f.askedEnvs = append(f.askedEnvs, req.EnvName)
	return &azdext.KeyValueResponse{
		Key:   req.Key,
		Value: f.values[req.EnvName][req.Key],
	}, nil
}

func twoEnvironments() *perEnv {
	return &perEnv{
		values: map[string]map[string]string{
			"default": {foundryEnvKey: "https://from-default/"},
			"staging": {foundryEnvKey: "https://from-staging/"},
		},
		current: "default",
	}
}

func TestSelectedEnvironmentIsTheOneRead(t *testing.T) {
	fake := twoEnvironments()

	ctx := WithSelectedEnvironment(context.Background(), "staging")
	value, name, err := readEnvHostedSource(ctx, fake)

	require.NoError(t, err)
	assert.Equal(t, "https://from-staging/", value)
	assert.Equal(t, "staging", name)
	assert.Zero(t, fake.currentCalls,
		"a named environment is the answer; asking azd for the current one can only disagree")
	assert.NotContains(t, fake.askedEnvs, "default")
}

func TestWithoutSelectionAzdsCurrentEnvironmentIsRead(t *testing.T) {
	fake := twoEnvironments()

	value, name, err := readEnvHostedSource(context.Background(), fake)

	require.NoError(t, err)
	assert.Equal(t, "https://from-default/", value)
	assert.Equal(t, "default", name)
	assert.Equal(t, 1, fake.currentCalls)
}

// A named environment holding no endpoint reports none. Falling back to the
// default's is the bug, restated.
func TestSelectedEnvironmentWithNoEndpointDoesNotFallBack(t *testing.T) {
	fake := twoEnvironments()
	fake.values["staging"] = map[string]string{}

	ctx := WithSelectedEnvironment(context.Background(), "staging")
	value, _, err := readEnvHostedSource(ctx, fake)

	require.NoError(t, err)
	assert.Empty(t, value, "staging has no endpoint; the default's is not an answer")
	assert.Zero(t, fake.currentCalls)
	assert.NotContains(t, fake.askedEnvs, "default")
}

// An empty name is "none given", not "the environment called empty string".
func TestWithSelectedEnvironmentIgnoresAnEmptyName(t *testing.T) {
	assert.Empty(t, SelectedEnvironment(WithSelectedEnvironment(context.Background(), "")))
	assert.Equal(t, "staging",
		SelectedEnvironment(WithSelectedEnvironment(context.Background(), "staging")))
	assert.Empty(t, SelectedEnvironment(context.Background()))
}
