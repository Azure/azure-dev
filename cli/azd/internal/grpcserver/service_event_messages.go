// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/commandresult"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// collectServiceEventMessages validates the structured messages on a final service handler status and adds them to
// the deploy command result. Stable and beta routes share these rules.
func collectServiceEventMessages(
	ctx context.Context,
	extensionID string,
	eventName string,
	serviceName string,
	status *azdext.ServiceHandlerStatus,
) error {
	if status == nil || len(status.GetMessages()) == 0 {
		return nil
	}
	if eventName != "predeploy" && eventName != "postdeploy" {
		return fmt.Errorf("messages are not supported for service event %q", eventName)
	}
	if status.GetEventName() != eventName || status.GetServiceName() != serviceName {
		return fmt.Errorf("service event messages do not match their invocation")
	}
	if status.GetStatus() != "completed" && status.GetStatus() != "failed" {
		return fmt.Errorf("service event messages require a final handler status")
	}

	collector := commandresult.ServiceEventMessageCollectorFromContext(ctx)
	if collector == nil {
		return fmt.Errorf("service event messages require a deploy command")
	}

	messages := make([]commandresult.ServiceEventMessage, 0, len(status.GetMessages()))
	for index, message := range status.GetMessages() {
		if message == nil {
			return fmt.Errorf("service event message %d is empty", index+1)
		}
		if strings.TrimSpace(message.GetMessage()) == "" {
			return fmt.Errorf("service event message %d has no text", index+1)
		}

		var kind string
		switch message.GetKind() {
		case azdext.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_INFO:
			kind = "info"
		case azdext.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING:
			kind = "warning"
		default:
			return fmt.Errorf(
				"service event message %d has unsupported kind %s",
				index+1,
				message.GetKind(),
			)
		}

		item := commandresult.ServiceEventMessage{
			ExtensionID: extensionID,
			ServiceName: serviceName,
			EventName:   eventName,
			Kind:        kind,
			Message:     message.GetMessage(),
			Suggestion:  message.GetSuggestion(),
		}
		for linkIndex, link := range message.GetLinks() {
			if link == nil || strings.TrimSpace(link.GetUrl()) == "" {
				return fmt.Errorf(
					"service event message %d link %d has no URL",
					index+1,
					linkIndex+1,
				)
			}
			linkURL, err := redactServiceEventMessageURL(link.GetUrl())
			if err != nil {
				return fmt.Errorf(
					"service event message %d link %d has an invalid URL",
					index+1,
					linkIndex+1,
				)
			}
			item.Links = append(item.Links, commandresult.ServiceEventMessageLink{
				Title: link.GetTitle(),
				URL:   linkURL,
			})
		}
		messages = append(messages, item)
	}

	collector.Add(messages)
	return nil
}

// redactServiceEventMessageURL removes credentials, query strings, and fragments from extension-provided links.
func redactServiceEventMessageURL(rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	parsedURL.User = nil
	parsedURL.RawQuery = ""
	parsedURL.ForceQuery = false
	parsedURL.Fragment = ""
	parsedURL.RawFragment = ""
	return parsedURL.String(), nil
}
