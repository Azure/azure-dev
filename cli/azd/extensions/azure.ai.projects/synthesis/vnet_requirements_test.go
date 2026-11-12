// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// VNet-injected Foundry projects need a project-level Agents capability host
// for private tracing to work; the platform only creates the account host.
func TestResourcesBicep_BYOProjectCapabilityHost(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("templates", "modules", "resources.bicep"))
	require.NoError(t, err)
	text := string(raw)

	require.Contains(t, text, "Microsoft.CognitiveServices/accounts/projects/capabilityHosts@")
	require.Contains(t, text, "= if (useByoNetwork) {", "host must be BYO-egress only")
	require.Contains(t, text, "capabilityHostKind: 'Agents'")
	require.Contains(t, text, "foundryAccount::project", "host must wait for the project")
	require.Contains(t, text, "byoFoundryAccount::project", "host must wait for the project")
}
