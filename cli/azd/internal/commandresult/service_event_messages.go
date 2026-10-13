// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package commandresult

import (
	"cmp"
	"context"
	"slices"
	"sync"
)

type serviceEventMessageCollectorKey struct{}

// ServiceEventMessage is a structured message from a deploy handler.
type ServiceEventMessage struct {
	ExtensionID string                    `json:"extensionId"`
	ServiceName string                    `json:"service"`
	EventName   string                    `json:"event"`
	Kind        string                    `json:"kind"`
	Message     string                    `json:"message"`
	Suggestion  string                    `json:"suggestion,omitempty"`
	Links       []ServiceEventMessageLink `json:"links,omitempty"`
}

// ServiceEventMessageLink references a deploy message.
type ServiceEventMessageLink struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
}

type collectedServiceEventMessage struct {
	message  ServiceEventMessage
	sequence uint64
}

// ServiceEventMessageCollector gathers messages for one command.
type ServiceEventMessageCollector struct {
	mu       sync.RWMutex
	messages []collectedServiceEventMessage
	sequence uint64
}

// NewServiceEventMessageCollector creates an empty command collector.
func NewServiceEventMessageCollector() *ServiceEventMessageCollector {
	return &ServiceEventMessageCollector{}
}

// WithServiceEventMessageCollector stores a context collector.
func WithServiceEventMessageCollector(
	ctx context.Context,
	collector *ServiceEventMessageCollector,
) context.Context {
	return context.WithValue(ctx, serviceEventMessageCollectorKey{}, collector)
}

// ServiceEventMessageCollectorFromContext returns the collector.
func ServiceEventMessageCollectorFromContext(ctx context.Context) *ServiceEventMessageCollector {
	collector, _ := ctx.Value(serviceEventMessageCollectorKey{}).(*ServiceEventMessageCollector)
	return collector
}

// Add appends one handler response without interleaving its messages.
func (c *ServiceEventMessageCollector) Add(messages []ServiceEventMessage) {
	if c == nil || len(messages) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, message := range messages {
		message.Links = slices.Clone(message.Links)
		c.sequence++
		c.messages = append(c.messages, collectedServiceEventMessage{
			message:  message,
			sequence: c.sequence,
		})
	}
}

// Snapshot returns messages in deployment-table and lifecycle order.
func (c *ServiceEventMessageCollector) Snapshot(serviceOrder []string) []ServiceEventMessage {
	if c == nil {
		return nil
	}

	c.mu.RLock()
	messages := slices.Clone(c.messages)
	c.mu.RUnlock()

	serviceRanks := make(map[string]int, len(serviceOrder))
	for index, serviceName := range serviceOrder {
		serviceRanks[serviceName] = index
	}

	slices.SortFunc(messages, func(left, right collectedServiceEventMessage) int {
		leftRank, leftExists := serviceRanks[left.message.ServiceName]
		rightRank, rightExists := serviceRanks[right.message.ServiceName]
		if leftExists != rightExists {
			if leftExists {
				return -1
			}
			return 1
		}
		if leftRank != rightRank {
			return cmp.Compare(leftRank, rightRank)
		}
		if left.message.ServiceName != right.message.ServiceName {
			return cmp.Compare(left.message.ServiceName, right.message.ServiceName)
		}

		leftEventRank := deployEventRank(left.message.EventName)
		rightEventRank := deployEventRank(right.message.EventName)
		if leftEventRank != rightEventRank {
			return cmp.Compare(leftEventRank, rightEventRank)
		}
		if left.message.EventName != right.message.EventName {
			return cmp.Compare(left.message.EventName, right.message.EventName)
		}
		if left.message.ExtensionID != right.message.ExtensionID {
			return cmp.Compare(left.message.ExtensionID, right.message.ExtensionID)
		}
		return cmp.Compare(left.sequence, right.sequence)
	})

	result := make([]ServiceEventMessage, len(messages))
	for index, message := range messages {
		result[index] = message.message
		result[index].Links = slices.Clone(message.message.Links)
	}
	return result
}

func deployEventRank(eventName string) int {
	switch eventName {
	case "predeploy":
		return 0
	case "postdeploy":
		return 1
	default:
		return 2
	}
}
