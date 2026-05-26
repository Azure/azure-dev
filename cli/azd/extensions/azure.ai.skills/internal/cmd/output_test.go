// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"azureaiskills/internal/pkg/skill_api"

	"github.com/stretchr/testify/require"
)

func TestWriteSkillTable(t *testing.T) {
	var buf bytes.Buffer
	skills := []skill_api.Skill{
		{Name: "alpha", Description: "first skill", DefaultVersion: "1", LatestVersion: "1"},
		{Name: "bravo", Description: "second skill with quite a long description that should be truncated", DefaultVersion: "2", LatestVersion: "3"},
	}
	require.NoError(t, writeSkillTable(&buf, skills))

	out := buf.String()
	require.True(t, strings.Contains(out, "NAME"), out)
	require.True(t, strings.Contains(out, "DEFAULT"), out)
	require.True(t, strings.Contains(out, "LATEST"), out)
	require.True(t, strings.Contains(out, "alpha"), out)
	require.True(t, strings.Contains(out, "bravo"), out)
	// Long description is truncated.
	require.True(t, strings.Contains(out, "..."), out)
}

func TestFormatUnix(t *testing.T) {
	require.Equal(t, "", formatUnix(0))
	require.Equal(t, "", formatUnix(-1))
	require.Equal(t, "1970-01-01T00:00:01Z", formatUnix(1))
}
