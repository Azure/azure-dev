// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeDebugLogMessage(t *testing.T) {
	//nolint:gosec // Synthetic credentials test debug log sanitization.
	input := `RESPONSE 400 Bad Request
x-ms-request-id: req-12345
{"CONNECTION_STRING": "Endpoint=sb://test", "error": {"code": "BadRequest", "message": "failed https://acct:fake-pw@host1.test/a and hTtPs://acct.blob.core.windows.net/c/b?sig=fake%2B-sig%3D%3D#frag-token"}}`

	got := sanitizeDebugLogMessage(input)

	assert.Contains(t, got, "RESPONSE 400 Bad Request")
	assert.Contains(t, got, "x-ms-request-id: req-12345")
	assert.Contains(t, got, `"CONNECTION_STRING": "REDACTED"`)
	assert.Contains(t, got, "https://host1.test/a")
	assert.Contains(t, got, "https://acct.blob.core.windows.net/c/b")
	for _, secret := range []string{"Endpoint=sb://test", "fake-pw", "sig=", "fake%2B-sig%3D%3D", "frag-token"} {
		assert.NotContains(t, got, secret)
	}
}

func TestSetupDebugLoggingRedactsResponseErrorURLs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", true, "")

	cleanup := setupDebugLogging(flags)
	t.Cleanup(cleanup)

	//nolint:gosec // Synthetic credentials test SDK response logging.
	body := `{"error":{"code":"BadRequest","message":"cannot fetch https://acct:fake-pw@example.test/pkg?sig=fake%2B-sig%3D%3D#frag-secret"}}`
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Header:     http.Header{"X-Ms-Request-Id": {"req-abc-123"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	_ = runtime.NewResponseError(resp)

	cleanup()

	matches, err := filepath.Glob(filepath.Join(tmpDir, "azd-ai-agents-*.log"))
	require.NoError(t, err)
	require.Len(t, matches, 1)

	logBytes, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	logText := string(logBytes)

	assert.Contains(t, logText, "400 Bad Request")
	assert.Contains(t, logText, "https://example.test/pkg")
	for _, secret := range []string{"fake-pw", "sig=", "fake%2B-sig%3D%3D", "frag-secret"} {
		assert.NotContains(t, logText, secret)
	}
}
