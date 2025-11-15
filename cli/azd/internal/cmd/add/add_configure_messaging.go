// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/internal/names"
	"github.com/azure/azure-dev/pkg/input"
	"github.com/azure/azure-dev/pkg/project"
)

func fillEventHubs(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	r.Name = "event-hubs"

	if _, exists := p.PrjConfig.Resources["event-hubs"]; exists {
		return nil, fmt.Errorf("only one event hubs resource is allowed at this time")
	}

	for {
		topicName, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: "Input the event hub name:",
			Help: "Event hub name\n\n" +
				"Name of the event hub that the app connects to. " +
				"Also known as a Kafka topic.",
		})
		if err != nil {
			return r, err
		}

		if err := names.ValidateLabelName(topicName); err != nil {
			console.Message(ctx, err.Error())
			continue
		}

		r.Props = project.EventHubsProps{
			Hubs: []string{topicName},
		}
		break
	}

	return r, nil
}

func fillServiceBus(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	r.Name = "service-bus"

	if _, exists := p.PrjConfig.Resources["service-bus"]; exists {
		return nil, fmt.Errorf("only one service bus resource is allowed at this time")
	}

	for {
		queueName, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: "Input the queue name:",
			Help: "Service Bus queue name\n\n" +
				"Name of the queue that the app connects to. ",
		})
		if err != nil {
			return r, err
		}

		if err := names.ValidateLabelName(queueName); err != nil {
			console.Message(ctx, err.Error())
			continue
		}

		r.Props = project.ServiceBusProps{
			Queues: []string{queueName},
		}
		break
	}

	return r, nil
}
