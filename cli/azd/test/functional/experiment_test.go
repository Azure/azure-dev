package cli_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// Verifies that the assignment context returned is included in the telemetry events we capture.
func Test_CLI_Experiment_AssignmentContextInTelemetry(t *testing.T) {
	t.Skip("Skipping while experimentation is not enabled")

	// CLI process and working directory are isolated
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	// Use a random config root so we don't get cached assigments from the real `azd` CLI.
	configRoot := tempDirWithDiagnostics(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/tas" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"assignmentContext":"context:393182;"}`))
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cli := azdcli.NewCLI(t)
	// Always set telemetry opt-inn setting to avoid influence from user settings
	cli.Env = append(
		os.Environ(),
		"AZURE_DEV_COLLECT_TELEMETRY=yes",
		"AZD_CONFIG_DIR="+configRoot,
		"AZD_DEBUG_EXPERIMENTATION_TAS_ENDPOINT="+fmt.Sprintf("%s/api/v1/tas", server.URL))
	cli.WorkingDirectory = dir

	envName := randomEnvName()

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	traceFilePath := filepath.Join(dir, "trace.json")

	_, err = cli.RunCommand(ctx, "env", "new", envName, "--trace-log-file", traceFilePath)
	require.NoError(t, err)
	fmt.Printf("envName: %s\n", envName)

	traceContent, err := os.ReadFile(traceFilePath)
	require.NoError(t, err)

	scanner := bufio.NewScanner(bytes.NewReader(traceContent))
	usageCmdFound := false
	for scanner.Scan() {
		if scanner.Text() == "" {
			continue
		}

		var span Span
		err = json.Unmarshal(scanner.Bytes(), &span)
		require.NoError(t, err)

		verifyResource(t, cli.Env, span.Resource)
		if strings.HasPrefix(span.Name, "cmd.") {
			usageCmdFound = true
			m := attributesMap(span.Attributes)
			require.Contains(t, m, fields.ExpAssignmentContextKey)
			require.Equal(t, "context:393182;", m[fields.ExpAssignmentContextKey])
		}
	}

	require.True(t, usageCmdFound)
}
