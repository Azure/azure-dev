// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOperationTelemetryDocsIncludeVoiceLive(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	require.True(t, ok)
	extensionRoot := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	checks := map[string][]string{
		filepath.Join(extensionRoot, "docs", "operation-telemetry.md"): {
			"voice_hosted_wrapper`, `voice_live`, `unknown", "81 possible tuples", `"voice_live", "unknown"`,
		},
		filepath.Join(extensionRoot, "docs", "operation-telemetry-fixture.kql"): {
			"agent.operation.v1.init.voice_live.none", `"voice_live", "unknown"`, "where telephony in",
		},
		filepath.Clean(filepath.Join(extensionRoot, "..", "..", "..", "..", "docs", "specs", "metrics-audit", "telemetry-schema.md")): {
			"voice_hosted_wrapper`, `voice_live`, `unknown`",
		},
	}
	for path, expected := range checks {
		content, err := os.ReadFile(path)
		require.NoError(t, err, path)
		for _, text := range expected {
			require.Contains(t, string(content), text, path)
		}
	}
}
