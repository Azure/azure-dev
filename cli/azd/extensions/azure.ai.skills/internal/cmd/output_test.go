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
		{Name: "alpha", Description: "first skill", HasBlob: false},
		{Name: "bravo", Description: "second skill with quite a long description that should be truncated", HasBlob: true},
	}
	require.NoError(t, writeSkillTable(&buf, skills))

	out := buf.String()
	require.True(t, strings.Contains(out, "NAME"), out)
	require.True(t, strings.Contains(out, "alpha"), out)
	require.True(t, strings.Contains(out, "bravo"), out)
	// Long description is truncated.
	require.True(t, strings.Contains(out, "..."), out)
}
