// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/commandresult"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func deploymentServiceOrder(services []*project.ServiceConfig) []string {
	order := make([]string, len(services))
	for index, service := range services {
		order[index] = service.Name
	}
	return order
}

func newDeploymentResult(
	state *deployGraphState,
	messages []commandresult.ServiceEventMessage,
) DeploymentResult {
	return DeploymentResult{
		Timestamp: time.Now(),
		Services:  state.ResultsSnapshot(),
		Messages:  messages,
	}
}

func formatDeploymentResult(
	formatter output.Formatter,
	writer io.Writer,
	state *deployGraphState,
	messages []commandresult.ServiceEventMessage,
) error {
	return formatter.Format(newDeploymentResult(state, messages), writer, nil)
}

func displayServiceEventMessages(
	ctx context.Context,
	console input.Console,
	messages []commandresult.ServiceEventMessage,
) {
	for _, message := range messages {
		description := fmt.Sprintf(
			"%s (%s): %s",
			message.ServiceName,
			message.EventName,
			message.Message,
		)
		details := serviceEventMessageDetails(message)

		if message.Kind == "warning" {
			console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: description,
				Hints:       details,
			})
			continue
		}

		lines := append([]string{description}, details...)
		console.MessageUxItem(ctx, &ux.MultilineMessage{Lines: lines})
	}
}

func serviceEventMessageDetails(
	message commandresult.ServiceEventMessage,
) []string {
	var details []string
	if message.Suggestion != "" {
		details = append(details, "  Suggestion: "+message.Suggestion)
	}
	for _, link := range message.Links {
		if link.Title == "" {
			details = append(details, "  "+output.WithLinkFormat(link.URL))
		} else {
			details = append(details, "  "+output.WithHyperlink(link.URL, link.Title))
		}
	}
	return details
}
