// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderBicepTemplate_FullSourcesMatchEmbeddedProvisioning(t *testing.T) {
	for _, name := range []string{
		"templates/main.bicep", "templates/modules/resources.bicep", "templates/modules/acr.bicep",
	} {
		t.Run(name, func(t *testing.T) {
			rendered, err := RenderBicepTemplate(name, true)
			require.NoError(t, err)
			embedded, err := TemplatesFS().ReadFile(name)
			require.NoError(t, err)
			assert.Equal(t, strings.ReplaceAll(string(embedded), "\r\n", "\n"),
				strings.ReplaceAll(string(rendered), "\r\n", "\n"),
				"regenerate the full provisioning sources with go generate after changing the shared templates")
		})
	}
}
