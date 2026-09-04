// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package commandresult

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
)

type followUpCollectorKey struct{}

// FollowUp is one explicit contribution from a completed
// project post* handler.
type FollowUp struct {
	ExtensionID string
	EventName   string
	Layer       string
	Text        string
}

type followUpKey struct {
	extensionID string
	eventName   string
	layer       string
}

// FollowUpCollector gathers extension follow-up text for one
// command.
type FollowUpCollector struct {
	mu            sync.RWMutex
	contributions map[followUpKey]string
}

// NewFollowUpCollector creates an empty command follow-up
// collector.
func NewFollowUpCollector() *FollowUpCollector {
	return &FollowUpCollector{
		contributions: make(map[followUpKey]string),
	}
}

// WithFollowUpCollector stores a collector in the command
// context.
func WithFollowUpCollector(ctx context.Context, collector *FollowUpCollector) context.Context {
	return context.WithValue(ctx, followUpCollectorKey{}, collector)
}

// FollowUpCollectorFromContext returns the collector, if
// present.
func FollowUpCollectorFromContext(ctx context.Context) *FollowUpCollector {
	collector, _ := ctx.Value(followUpCollectorKey{}).(*FollowUpCollector)
	return collector
}

// Add records an explicit follow-up. Empty text retracts that
// extension's value for this event and layer. Callers must
// invoke Add only when follow_up was set.
func (c *FollowUpCollector) Add(item FollowUp) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contributions == nil {
		c.contributions = make(map[followUpKey]string)
	}
	c.contributions[followUpKey{
		extensionID: item.ExtensionID,
		eventName:   item.EventName,
		layer:       item.Layer,
	}] = item.Text
}

// postEventRank orders known project post* events. Later
// events replace earlier ones for the same extension.
var postEventRank = map[string]int{
	"postrestore":   1,
	"postprovision": 2,
	"postbuild":     3,
	"postpackage":   4,
	"postpublish":   5,
	"postdeploy":    6,
}

func eventOrder(eventName string) (int, string) {
	if rank, ok := postEventRank[eventName]; ok {
		return rank, eventName
	}
	return len(postEventRank) + 1, eventName
}

func laterFollowUp(candidate, current followUpKey) bool {
	candidateRank, candidateEvent := eventOrder(candidate.eventName)
	currentRank, currentEvent := eventOrder(current.eventName)
	if candidateRank != currentRank {
		return candidateRank > currentRank
	}
	if candidateEvent != currentEvent {
		return candidateEvent > currentEvent
	}
	return candidate.layer > current.layer
}

// Text resolves contributions after the command. For each
// extension, the latest lifecycle event wins. Within that
// event, the last layer in lexicographic order wins. Empty
// winning text retracts the extension. Remaining texts are
// joined in extension ID order.
func (c *FollowUpCollector) Text() string {
	if c == nil {
		return ""
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	winners := make(map[string]followUpKey, len(c.contributions))
	values := make(map[string]string, len(c.contributions))
	for key, text := range c.contributions {
		current, ok := winners[key.extensionID]
		if !ok || laterFollowUp(key, current) {
			winners[key.extensionID] = key
			values[key.extensionID] = text
		}
	}

	contributions := make([]string, 0, len(winners))
	for _, extensionID := range slices.Sorted(maps.Keys(winners)) {
		if followUp := strings.TrimSpace(values[extensionID]); followUp != "" {
			contributions = append(contributions, followUp)
		}
	}

	return strings.Join(contributions, "\n\n")
}
