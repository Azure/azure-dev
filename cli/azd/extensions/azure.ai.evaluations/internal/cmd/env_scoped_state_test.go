// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envScopedServer records which environment each config call named, and keeps a
// separate section per environment so a read from the wrong one is visible.
type envScopedServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	sections map[string]map[string][]byte
	readFrom []string
	wroteTo  []string
}

func (s *envScopedServer) GetConfig(
	_ context.Context, req *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	s.readFrom = append(s.readFrom, req.EnvName)
	value, found := s.sections[req.EnvName][req.Path]
	return &azdext.GetConfigResponse{Value: value, Found: found}, nil
}

func (s *envScopedServer) SetConfig(
	_ context.Context, req *azdext.SetConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.wroteTo = append(s.wroteTo, req.EnvName)
	if s.sections == nil {
		s.sections = map[string]map[string][]byte{}
	}
	if s.sections[req.EnvName] == nil {
		s.sections[req.EnvName] = map[string][]byte{}
	}
	s.sections[req.EnvName][req.Path] = req.Value
	return &azdext.EmptyResponse{}, nil
}

// azdext's ConfigHelper sends no environment name, so it reads and writes azd's
// current one whatever -e asked for. `-e staging` therefore took production's
// fingerprints, decided a dataset was unchanged, and recorded its own results
// over them.
func TestPrivateStateIsReadFromTheEnvironmentAskedFor(t *testing.T) {
	prod, err := json.Marshal(map[string]string{
		stateEndpointKey: "https://p.example",
		"DATASET_GOLDEN": "prod-digest",
	})
	require.NoError(t, err)
	staging, err := json.Marshal(map[string]string{
		stateEndpointKey: "https://p.example",
		"DATASET_GOLDEN": "staging-digest",
	})
	require.NoError(t, err)

	srv := &envScopedServer{sections: map[string]map[string][]byte{
		"prod":    {privateStatePath: prod},
		"staging": {privateStatePath: staging},
	}}

	ec := &evalContext{
		azdClient: newTestAzdClient(t, srv),
		envName:   "staging",
		endpoint:  "https://p.example",
	}

	assert.Equal(t, "staging-digest", ec.privateValue(t.Context(), "DATASET_GOLDEN"),
		"the state has to come from the environment the command was pointed at")
	assert.Equal(t, []string{"staging"}, srv.readFrom,
		"and the read has to name it rather than leaving azd to pick the default")
}

// The write goes back where the read came from, or the next command reads a
// value this one never recorded.
func TestPrivateStateIsWrittenToTheEnvironmentAskedFor(t *testing.T) {
	srv := &envScopedServer{}
	ec := &evalContext{
		azdClient: newTestAzdClient(t, srv),
		envName:   "staging",
		endpoint:  "https://p.example",
	}

	require.NoError(t, ec.setPrivate(t.Context(), "DATASET_GOLDEN", "new-digest"))

	assert.Equal(t, []string{"staging"}, srv.wroteTo)
	require.Contains(t, srv.sections, "staging")

	var stored map[string]string
	require.NoError(t, json.Unmarshal(srv.sections["staging"][privateStatePath], &stored))
	assert.Equal(t, "new-digest", stored["DATASET_GOLDEN"])
	assert.Equal(t, "https://p.example", stored[stateEndpointKey],
		"the endpoint the state belongs to is recorded with it")
}

// State left by another Foundry project is discarded rather than inherited: the
// section lives in the azd environment, but everything in it is only true of
// one project.
func TestPrivateStateFromAnotherEndpointIsNotInherited(t *testing.T) {
	other, err := json.Marshal(map[string]string{
		stateEndpointKey: "https://a.example",
		"DATASET_GOLDEN": "a-digest",
	})
	require.NoError(t, err)

	srv := &envScopedServer{sections: map[string]map[string][]byte{
		"shared": {privateStatePath: other},
	}}
	ec := &evalContext{
		azdClient: newTestAzdClient(t, srv),
		envName:   "shared",
		endpoint:  "https://b.example/",
	}

	assert.Empty(t, ec.privateValue(t.Context(), "DATASET_GOLDEN"),
		"project B must not reuse project A's fingerprint")
}

// A trailing slash or a change of case is the same project, so the state is not
// thrown away over how the endpoint was typed.
func TestPrivateStateSurvivesAnEquivalentEndpoint(t *testing.T) {
	same, err := json.Marshal(map[string]string{
		stateEndpointKey: "https://p.example",
		"DATASET_GOLDEN": "digest",
	})
	require.NoError(t, err)

	srv := &envScopedServer{sections: map[string]map[string][]byte{
		"e": {privateStatePath: same},
	}}
	ec := &evalContext{
		azdClient: newTestAzdClient(t, srv),
		envName:   "e",
		endpoint:  "HTTPS://P.example/",
	}

	assert.Equal(t, "digest", ec.privateValue(t.Context(), "DATASET_GOLDEN"))
}
