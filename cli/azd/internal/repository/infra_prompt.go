package repository

import (
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
)

type infraPrompt interface {
	Type() string
	Properties() map[string]string
	Apply(spec *scaffold.InfraSpec)
}

type serviceBusPrompt struct {
	name                  string
	queues                []string
	topicAndSubscriptions []string
}

func (s *serviceBusPrompt) Type() string {
	return appdetect.AzureDepServiceBus{}.ResourceDisplay()
}

func (s *serviceBusPrompt) Properties() map[string]string {
	return map[string]string{
		"name":                  "Service Bus namespace name",
		"queues":                "Comma-separated list of queue names",
		"topicAndSubscriptions": "Comma-separated list of topic names and their subscriptions, of format 'topicName:subscription1,subscription2,...'",
	}
}

func (s *serviceBusPrompt) Apply(spec *scaffold.InfraSpec) {
	if spec.AzureServiceBus == nil {
		spec.AzureServiceBus = &scaffold.AzureDepServiceBus{}
	}
	spec.AzureServiceBus.Name = s.name
	spec.AzureServiceBus.Queues = s.queues
}
