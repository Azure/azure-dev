// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/commandresult"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestFormatDeploymentResultIncludesServiceEventMessages(t *testing.T) {
	state := newDeployGraphState([]*project.ServiceConfig{{Name: "api"}})
	state.StoreResult("api", &project.ServiceDeployResult{})
	messages := []commandresult.ServiceEventMessage{{
		ExtensionID: "test.extension",
		ServiceName: "api",
		EventName:   "postdeploy",
		Kind:        "warning",
		Message:     "Review the access policy.",
		Suggestion:  "Update the policy before the next deployment.",
		Links: []commandresult.ServiceEventMessageLink{{
			Title: "Deployment guide",
			URL:   "https://example.com/deploy",
		}},
	}}
	var writer bytes.Buffer

	err := formatDeploymentResult(&output.JsonFormatter{}, &writer, state, messages)
	require.NoError(t, err)

	var result DeploymentResult
	require.NoError(t, json.Unmarshal(writer.Bytes(), &result))
	require.Equal(t, messages, result.Messages)
	require.Contains(t, result.Services, "api")
}

func TestDisplayServiceEventMessages(t *testing.T) {
	console := mockinput.NewMockConsole()
	messages := []commandresult.ServiceEventMessage{
		{
			ServiceName: "api",
			EventName:   "postdeploy",
			Kind:        "warning",
			Message:     "Review the access policy.",
			Suggestion:  "Update the policy.",
		},
		{
			ServiceName: "web",
			EventName:   "predeploy",
			Kind:        "info",
			Message:     "Preparing for deployment.",
		},
	}

	displayServiceEventMessages(t.Context(), console, messages)

	output := console.Output()
	require.Len(t, output, 2)
	require.Contains(t, output[0], "WARNING: api (postdeploy): Review the access policy.")
	require.Contains(t, output[0], "Suggestion: Update the policy.")
	require.Contains(t, strings.Join(output, "\n"), "web (predeploy): Preparing for deployment.")
}
