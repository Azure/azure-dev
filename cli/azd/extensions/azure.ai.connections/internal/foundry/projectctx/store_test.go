// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProjectContextConfigPath pins the global-config key shared with
// azure.ai.agents (writer), azure.ai.projects, and azure.ai.toolboxes
// (readers). Changing this string silently is a cross-extension break;
// require an explicit test update.
func TestProjectContextConfigPath(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "extensions.ai-agents.project.context", projectContextConfigPath)
}
