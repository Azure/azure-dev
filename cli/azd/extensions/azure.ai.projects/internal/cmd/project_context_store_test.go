// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeProjectContextConfig struct {
	values    map[string]projectContextState
	unsetErrs map[string]error
	unsetKeys []string
}

func (f *fakeProjectContextConfig) GetUserJSON(_ context.Context, path string, out any) (bool, error) {
	state, ok := f.values[path]
	if !ok {
		return false, nil
	}

	*out.(*projectContextState) = state
	return true, nil
}

func (f *fakeProjectContextConfig) UnsetUser(_ context.Context, path string) error {
	f.unsetKeys = append(f.unsetKeys, path)
	if err := f.unsetErrs[path]; err != nil {
		return err
	}

	delete(f.values, path)
	return nil
}

func TestClearProjectContextFromConfig_ClearsCanonicalAndLegacy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		values       map[string]projectContextState
		wantPrevious string
	}{
		{
			name: "canonical only",
			values: map[string]projectContextState{
				projectContextConfigPath: {Endpoint: "https://new.services.ai.azure.com/api/projects/p"},
			},
			wantPrevious: "https://new.services.ai.azure.com/api/projects/p",
		},
		{
			name: "legacy only",
			values: map[string]projectContextState{
				legacyAgentsContextPath: {Endpoint: "https://old.services.ai.azure.com/api/projects/p"},
			},
			wantPrevious: "https://old.services.ai.azure.com/api/projects/p",
		},
		{
			name: "canonical wins previous endpoint",
			values: map[string]projectContextState{
				projectContextConfigPath: {Endpoint: "https://new.services.ai.azure.com/api/projects/p"},
				legacyAgentsContextPath:  {Endpoint: "https://old.services.ai.azure.com/api/projects/p"},
			},
			wantPrevious: "https://new.services.ai.azure.com/api/projects/p",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &fakeProjectContextConfig{
				values:    tt.values,
				unsetErrs: map[string]error{},
			}

			previous, err := clearProjectContextFromConfig(t.Context(), cfg)

			require.NoError(t, err)
			assert.Equal(t, tt.wantPrevious, previous)
			assert.Equal(t, []string{projectContextConfigPath, legacyAgentsContextPath}, cfg.unsetKeys)
			assert.Empty(t, cfg.values)
		})
	}
}

func TestClearProjectContextFromConfig_ReturnsLegacyClearError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("legacy unset failed")
	cfg := &fakeProjectContextConfig{
		values: map[string]projectContextState{
			legacyAgentsContextPath: {Endpoint: "https://old.services.ai.azure.com/api/projects/p"},
		},
		unsetErrs: map[string]error{
			legacyAgentsContextPath: sentinel,
		},
	}

	previous, err := clearProjectContextFromConfig(t.Context(), cfg)

	require.ErrorIs(t, err, sentinel)
	assert.Empty(t, previous)
	assert.Equal(t, []string{projectContextConfigPath, legacyAgentsContextPath}, cfg.unsetKeys)
}
