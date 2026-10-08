// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProjectContextConfigPath pins the global-config key owned by
// azure.ai.projects and read by the other Foundry extensions. Changing this
// string silently is a cross-extension break; require an explicit test update.
func TestProjectContextConfigPath(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "extensions.ai-projects.context", projectContextConfigPath)
	assert.Equal(t, "extensions.ai-agents.project.context", legacyProjectContextConfigPath)
}

type fakeProjectContextConfig struct {
	values map[string]State
	errors map[string]error
}

func (f fakeProjectContextConfig) GetUserJSON(_ context.Context, path string, out any) (bool, error) {
	if err := f.errors[path]; err != nil {
		return false, err
	}
	value, found := f.values[path]
	if found {
		*out.(*State) = value
	}
	return found, nil
}

func TestReadProjectContext(t *testing.T) {
	t.Parallel()

	canonical := State{Endpoint: "https://canonical.services.ai.azure.com/api/projects/p"}
	legacy := State{Endpoint: "https://legacy.services.ai.azure.com/api/projects/p"}

	t.Run("canonical wins", func(t *testing.T) {
		state, found, err := readProjectContext(t.Context(), fakeProjectContextConfig{values: map[string]State{
			projectContextConfigPath:       canonical,
			legacyProjectContextConfigPath: legacy,
		}})
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, canonical, state)
	})

	t.Run("legacy fallback", func(t *testing.T) {
		state, found, err := readProjectContext(t.Context(), fakeProjectContextConfig{values: map[string]State{
			legacyProjectContextConfigPath: legacy,
		}})
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, legacy, state)
	})

	t.Run("canonical error is returned", func(t *testing.T) {
		_, _, err := readProjectContext(t.Context(), fakeProjectContextConfig{errors: map[string]error{
			projectContextConfigPath: errors.New("invalid canonical context"),
		}})
		assert.Error(t, err)
	})

	// A missing legacy key answers (false, nil), so an error here means the
	// context is there and could not be read. Stepping over it resolves the
	// endpoint from a lower-priority source -- possibly another project.
	t.Run("legacy that cannot be read is reported, not stepped over", func(t *testing.T) {
		_, found, err := readProjectContext(t.Context(), fakeProjectContextConfig{errors: map[string]error{
			legacyProjectContextConfigPath: errors.New("invalid legacy context"),
		}})
		assert.Error(t, err, "an unreadable context must not look like no context")
		assert.False(t, found)
	})

	// The migration deletes the key, so every migrated config misses here. That
	// is the ordinary path and carries no error.
	t.Run("legacy simply absent is not an error", func(t *testing.T) {
		state, found, err := readProjectContext(t.Context(), fakeProjectContextConfig{})
		assert.NoError(t, err)
		assert.False(t, found)
		assert.Empty(t, state)
	})
}
