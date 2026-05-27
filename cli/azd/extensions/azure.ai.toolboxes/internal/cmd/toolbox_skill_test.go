// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSkillFlag(t *testing.T) {
	t.Run("bare name", func(t *testing.T) {
		spec, err := parseSkillFlag("my-skill")
		require.NoError(t, err)
		assert.Equal(t, "my-skill", spec.Name)
		assert.Empty(t, spec.Version)
	})

	t.Run("name with version", func(t *testing.T) {
		spec, err := parseSkillFlag("my-skill@2")
		require.NoError(t, err)
		assert.Equal(t, "my-skill", spec.Name)
		assert.Equal(t, "2", spec.Version)
	})

	t.Run("version with whitespace trimmed", func(t *testing.T) {
		spec, err := parseSkillFlag("  qa-skill@  v1.0.0  ")
		require.NoError(t, err)
		assert.Equal(t, "qa-skill", spec.Name)
		assert.Equal(t, "v1.0.0", spec.Version)
	})

	t.Run("empty rejected", func(t *testing.T) {
		_, err := parseSkillFlag("")
		requireLocalError(t, err, exterrors.CodeInvalidSkillSpec)
	})

	t.Run("whitespace-only rejected", func(t *testing.T) {
		_, err := parseSkillFlag("   ")
		requireLocalError(t, err, exterrors.CodeInvalidSkillSpec)
	})

	t.Run("trailing @ rejected", func(t *testing.T) {
		_, err := parseSkillFlag("my-skill@")
		requireLocalError(t, err, exterrors.CodeInvalidSkillSpec)
	})

	t.Run("trailing @whitespace rejected", func(t *testing.T) {
		_, err := parseSkillFlag("my-skill@   ")
		requireLocalError(t, err, exterrors.CodeInvalidSkillSpec)
	})

	t.Run("uppercase name rejected", func(t *testing.T) {
		_, err := parseSkillFlag("MySkill")
		requireLocalError(t, err, exterrors.CodeInvalidSkillName)
	})

	t.Run("leading hyphen rejected", func(t *testing.T) {
		_, err := parseSkillFlag("-skill")
		requireLocalError(t, err, exterrors.CodeInvalidSkillName)
	})

	t.Run("trailing hyphen rejected", func(t *testing.T) {
		_, err := parseSkillFlag("skill-")
		requireLocalError(t, err, exterrors.CodeInvalidSkillName)
	})

	t.Run("underscore rejected", func(t *testing.T) {
		_, err := parseSkillFlag("my_skill")
		requireLocalError(t, err, exterrors.CodeInvalidSkillName)
	})

	t.Run("over 64 chars rejected", func(t *testing.T) {
		long := ""
		for range 65 {
			long += "a"
		}
		_, err := parseSkillFlag(long)
		requireLocalError(t, err, exterrors.CodeInvalidSkillName)
	})

	t.Run("exactly 64 chars accepted", func(t *testing.T) {
		long := ""
		for range 64 {
			long += "a"
		}
		spec, err := parseSkillFlag(long)
		require.NoError(t, err)
		assert.Equal(t, long, spec.Name)
	})
}

func TestBuildSkillEntry(t *testing.T) {
	t.Run("with version", func(t *testing.T) {
		entry := buildSkillEntry(skillSpec{Name: "my-skill", Version: "2"})
		assert.Equal(t, "skill_reference", entry["type"])
		assert.Equal(t, "my-skill", entry["name"])
		assert.Equal(t, "2", entry["version"])
	})

	t.Run("without version omits version key", func(t *testing.T) {
		entry := buildSkillEntry(skillSpec{Name: "my-skill"})
		assert.Equal(t, "skill_reference", entry["type"])
		assert.Equal(t, "my-skill", entry["name"])
		_, hasVersion := entry["version"]
		assert.False(t, hasVersion, "version key must be omitted when empty")
	})
}

func TestValidateNoDuplicateSkills(t *testing.T) {
	t.Run("unique names pass", func(t *testing.T) {
		err := validateNoDuplicateSkills([]map[string]any{
			{"type": "skill_reference", "name": "a"},
			{"type": "skill_reference", "name": "b"},
			{"type": "skill_reference", "name": "c"},
		})
		require.NoError(t, err)
	})

	t.Run("duplicate names rejected", func(t *testing.T) {
		err := validateNoDuplicateSkills([]map[string]any{
			{"type": "skill_reference", "name": "dup"},
			{"type": "skill_reference", "name": "other"},
			{"type": "skill_reference", "name": "dup"},
		})
		le := requireLocalError(t, err, exterrors.CodeDuplicateSkill)
		assert.Contains(t, le.Message, "dup")
	})

	t.Run("duplicates differ in version still rejected", func(t *testing.T) {
		// Pinning the same skill to two different versions is also a duplicate
		// for our purposes; the service is single-row-per-name.
		err := validateNoDuplicateSkills([]map[string]any{
			{"type": "skill_reference", "name": "x", "version": "1"},
			{"type": "skill_reference", "name": "x", "version": "2"},
		})
		requireLocalError(t, err, exterrors.CodeDuplicateSkill)
	})

	t.Run("empty list accepted", func(t *testing.T) {
		require.NoError(t, validateNoDuplicateSkills(nil))
		require.NoError(t, validateNoDuplicateSkills([]map[string]any{}))
	})
}
