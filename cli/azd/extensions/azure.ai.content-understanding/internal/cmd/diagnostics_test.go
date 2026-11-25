// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticColorsFollowAzdConventions(t *testing.T) {
	setHelpColor(t, true)
	for _, test := range []struct {
		name, format, noColor string
		colored               bool
	}{
		{"human", "default", "", true},
		{"NO_COLOR", "default", "1", false},
		{"JSON", "json", "", false},
		{"none", "none", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", test.noColor)
			const message = "A diagnostic is 50% complete."
			warning, failure := warningText(test.format, message), errorText(test.format, message)
			action := actionText(test.format, message)
			comment := commentText(test.format, message)
			if test.colored {
				assert.Equal(t, output.WithWarningFormat("%s", message), warning)
				assert.Contains(t, warning, "\x1b[33m")
				assert.Equal(t, output.WithErrorFormat("%s", message), failure)
				assert.Contains(t, failure, "\x1b[31m")
				assert.Equal(t, output.WithHighLightFormat("%s", message), action)
				assert.Contains(t, action, "\x1b[94m")
				assert.Equal(t, output.WithGrayFormat("%s", message), comment)
				assert.Contains(t, comment, "\x1b[90m")
			} else {
				assert.Equal(t, message, warning)
				assert.Equal(t, message, failure)
				assert.Equal(t, message, action)
				assert.Equal(t, message, comment)
			}
		})
	}
	setHelpColor(t, false)
	t.Setenv("NO_COLOR", "")
	assert.Equal(t, "Warning", warningText("default", "Warning"))
	assert.Equal(t, "Error", errorText("default", "Error"))
	assert.Equal(t, "Action", actionText("default", "Action"))
	assert.Equal(t, "Comment", commentText("default", "Comment"))
}

func TestWarningUsesInjectedWriterAndPropagatesFailures(t *testing.T) {
	setHelpColor(t, true)
	t.Setenv("NO_COLOR", "")
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	require.NoError(t, writeWarning(cmd, "default", "Review the configuration."))
	assert.Empty(t, stdout.String())
	assert.Equal(t, output.WithWarningFormat("%s", "Warning: Review the configuration.")+"\n", stderr.String())
	stderr.Reset()
	require.NoError(t, writeWarning(cmd, "json", "Review the configuration."))
	assert.Equal(t, "Warning: Review the configuration.\n", stderr.String())
	cmd.SetErr(failingWriter{})
	require.Error(t, writeWarning(cmd, "default", "Review the configuration."))
}

func TestPostprovisionWarningColorsPreserveStructuredResults(t *testing.T) {
	setHelpColor(t, true)
	t.Setenv("NO_COLOR", "")
	for _, format := range []string{"default", "json", "none"} {
		t.Run(format, func(t *testing.T) {
			host := infraCommandFixture(t)
			host.capabilities.fail = "setup"
			stdout, stderr, err := executeInfra(t, host, "infra", "_postprovision-v1", "--output", format)
			require.NoError(t, err)
			assert.Contains(t, stderr, "Warning: Optional model setup failed.")
			if format == "default" {
				assert.Contains(t, stderr, "\x1b[33m")
			} else {
				assert.NotContains(t, stderr, "\x1b")
			}
			assert.NotContains(t, stdout, "\x1b")
			host.capabilities.fail = "endpoint"
			stdout, stderr, err = executeInfra(t, host, "infra", "_postprovision-v1", "--output", format)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "\x1b", "the host owns rendering structured command errors")
			assert.Empty(t, stdout)
		})
	}
}

func TestSDKDiagnosticsContainOnlySafeEventCategories(t *testing.T) {
	root := NewRootCommand()
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	listener := SDKLogListener(root)
	listener(log.EventRequest, "Authorization: Custom private-token")
	assert.Empty(t, stderr.String())
	require.NoError(t, root.PersistentFlags().Set("debug", "true"))
	var workers sync.WaitGroup
	for _, event := range []log.Event{
		log.EventRequest, log.EventResponse, log.EventResponseError, log.EventRetryPolicy, log.EventLRO, "private-event",
	} {
		workers.Go(func() {
			listener(event, "private-header https://private-host/private-path?sig=private-token private-content")
		})
	}
	workers.Wait()
	assert.Contains(t, stderr.String(), "Azure SDK")
	assert.Contains(t, stderr.String(), "details suppressed")
	assert.NotContains(t, stderr.String(), "private")
}

func TestArgumentAndPreRunErrorsDoNotEchoCustomerValues(t *testing.T) {
	for _, args := range [][]string{
		{"analyze", "--concurrency", "private-token"},
		{"version", "private-doc.pdf"},
		{"analyzer", "list", "--private-option"},
		{"version", "--cwd", "/private-missing-directory"},
	} {
		root := NewRootCommand()
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs(args)
		err := root.ExecuteContext(t.Context())
		require.Error(t, err)
		assert.NotContains(t, err.Error()+stderr.String()+stdout.String(), "private")
	}
	root := NewRootCommand()
	err := ValidateInvocation(root, []string{"private-unknown-command"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private")
	require.NoError(t, ValidateInvocation(root, []string{"analyze", "--help"}))
}
