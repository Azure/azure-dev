// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
