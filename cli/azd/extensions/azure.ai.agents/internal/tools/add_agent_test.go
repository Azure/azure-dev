// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnifiedInitGuidanceUsesTemplateFlag(t *testing.T) {
	guidance := unifiedInitGuidance(`C:\agent templates\azure.yaml`)

	require.Contains(t, guidance, `azd ai agent init -t "C:\\agent templates\\azure.yaml"`)
	require.NotContains(t, guidance, "azd ai agent init -m")
}
