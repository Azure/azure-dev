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
	setErrs   map[string]error
	unsetErrs map[string]error
	setKeys   []string
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

func (f *fakeProjectContextConfig) SetUserJSON(_ context.Context, path string, value any) error {
	f.setKeys = append(f.setKeys, path)
	if err := f.setErrs[path]; err != nil {
		return err
	}

	state, ok := value.(projectContextState)
	if !ok {
		return errors.New("fakeProjectContextConfig.SetUserJSON: unexpected value type")
	}

	if f.values == nil {
		f.values = map[string]projectContextState{}
	}
	f.values[path] = state
	return nil
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

func TestWriteMigratedProjectContext_CopiesLegacyAndClearsIt(t *testing.T) {
	t.Parallel()

	state := projectContextState{
		Endpoint: "https://legacy.services.ai.azure.com/api/projects/p",
		SetAt:    "2024-12-31T23:59:59Z",
	}
	cfg := &fakeProjectContextConfig{
		values: map[string]projectContextState{
			legacyAgentsContextPath: state,
		},
	}

	require.NoError(t, writeMigratedProjectContext(t.Context(), cfg, state))

	assert.Equal(t, []string{projectContextConfigPath}, cfg.setKeys)
	assert.Equal(t, []string{legacyAgentsContextPath}, cfg.unsetKeys)
	assert.Equal(t, state, cfg.values[projectContextConfigPath])
	_, legacyStillPresent := cfg.values[legacyAgentsContextPath]
	assert.False(t, legacyStillPresent, "legacy key must be cleared after migration")
}

func TestWriteMigratedProjectContext_SetFailureLeavesLegacyKey(t *testing.T) {
	t.Parallel()

	state := projectContextState{
		Endpoint: "https://legacy.services.ai.azure.com/api/projects/p",
	}
	sentinel := errors.New("set failed")
	cfg := &fakeProjectContextConfig{
		values: map[string]projectContextState{
			legacyAgentsContextPath: state,
		},
		setErrs: map[string]error{
			projectContextConfigPath: sentinel,
		},
	}

	err := writeMigratedProjectContext(t.Context(), cfg, state)

	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, []string{projectContextConfigPath}, cfg.setKeys)
	assert.Empty(t, cfg.unsetKeys,
		"legacy key must stay until the new key is successfully written")
	assert.Equal(t, state, cfg.values[legacyAgentsContextPath])
}

func TestWriteMigratedProjectContext_UnsetFailureBubblesUp(t *testing.T) {
	t.Parallel()

	state := projectContextState{
		Endpoint: "https://legacy.services.ai.azure.com/api/projects/p",
	}
	sentinel := errors.New("unset failed")
	cfg := &fakeProjectContextConfig{
		values: map[string]projectContextState{
			legacyAgentsContextPath: state,
		},
		unsetErrs: map[string]error{
			legacyAgentsContextPath: sentinel,
		},
	}

	err := writeMigratedProjectContext(t.Context(), cfg, state)

	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, []string{projectContextConfigPath}, cfg.setKeys)
	assert.Equal(t, []string{legacyAgentsContextPath}, cfg.unsetKeys)
	// New key was written even though the legacy unset failed; the caller
	// will retry the cleanup on a subsequent run because the legacy key
	// remains present.
	assert.Equal(t, state, cfg.values[projectContextConfigPath])
	assert.Equal(t, state, cfg.values[legacyAgentsContextPath])
}
