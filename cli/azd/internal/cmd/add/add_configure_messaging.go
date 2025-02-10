// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func fillEventHubs(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	if r.Name == "" {
		r.Name = "eventhubs"
	}

	if err := validateResourceName(r.Name, p.PrjConfig); err != nil {
		return r, errors.New("only one event hubs resource is currently allowed")
	}

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
		return r, err
	}

	r.Props = project.EventHubsProps{
		Hubs: []string{topicName},
	}

	return r, nil
}

func fillServiceBus(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	if r.Name == "" {
		r.Name = "servicebus"
	}

	if err := validateResourceName(r.Name, p.PrjConfig); err != nil {
		return r, errors.New("only one service bus resource is currently allowed")
	}

	queueName, err := console.Prompt(ctx, input.ConsoleOptions{
		Message: "Input the queue name:",
		Help: "Service Bus queue name\n\n" +
			"Name of the queue that the app connects to. ",
	})
	if err != nil {
		return r, err
	}

	if err := names.ValidateLabelName(queueName); err != nil {
		return r, err
	}

	r.Props = project.ServiceBusProps{
		Queues: []string{queueName},
	}

	return r, nil
}
