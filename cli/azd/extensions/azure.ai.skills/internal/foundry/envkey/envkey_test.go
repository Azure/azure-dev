// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkey

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSkillVersion(t *testing.T) {
	t.Parallel()
	require.Equal(t, "SKILL_SUMMARIZE_TOOLS_VERSION", SkillVersion("summarize-tools"))
	require.Equal(t, "SKILL_MY__SKILL_VERSION", SkillVersion("my--skill"))
	require.Equal(t, "SKILL_SUMMARIZE_TOOLS_PROJECT_ENDPOINT", SkillProjectEndpoint("summarize-tools"))
}
