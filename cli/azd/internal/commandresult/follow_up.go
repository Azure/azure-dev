// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package commandresult

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
)

type followUpCollectorKey struct{}

type followUpContributionKey struct {
	eventName  string
	instanceID string
}

type followUpContribution struct {
	eventName  string
	instanceID string
	text       string
}

// FollowUpCollector gathers extension follow-up text for one command.
type FollowUpCollector struct {
	mu            sync.RWMutex
	contributions map[string]map[followUpContributionKey]string
}

// NewFollowUpCollector creates an empty command follow-up collector.
func NewFollowUpCollector() *FollowUpCollector {
	return &FollowUpCollector{
		contributions: make(map[string]map[followUpContributionKey]string),
	}
}

// WithFollowUpCollector stores a collector in the command context.
func WithFollowUpCollector(ctx context.Context, collector *FollowUpCollector) context.Context {
	return context.WithValue(ctx, followUpCollectorKey{}, collector)
}

// FollowUpCollectorFromContext returns the collector, if present.
func FollowUpCollectorFromContext(ctx context.Context) *FollowUpCollector {
	collector, _ := ctx.Value(followUpCollectorKey{}).(*FollowUpCollector)
	return collector
}

// Record records an explicit follow-up value for an extension event.
// A nil value does not change the existing contribution.
func (c *FollowUpCollector) Record(
	extensionID string,
	eventName string,
	instanceID string,
	followUp *string,
) {
	if c == nil || followUp == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contributions == nil {
		c.contributions = make(map[string]map[followUpContributionKey]string)
	}
	if c.contributions[extensionID] == nil {
		c.contributions[extensionID] =
			make(map[followUpContributionKey]string)
	}
	c.contributions[extensionID][followUpContributionKey{
		eventName:  eventName,
		instanceID: instanceID,
	}] = *followUp
}

// Text returns contributions in deterministic lifecycle and extension order.
func (c *FollowUpCollector) Text() string {
	if c == nil {
		return ""
	}

	c.mu.RLock()
	contributionsByExtension := make(
		map[string][]followUpContribution,
		len(c.contributions),
	)
	for extensionID, contributions := range c.contributions {
		entries := make([]followUpContribution, 0, len(contributions))
		for key, text := range contributions {
			entries = append(entries, followUpContribution{
				eventName:  key.eventName,
				instanceID: key.instanceID,
				text:       text,
			})
		}
		contributionsByExtension[extensionID] = entries
	}
	c.mu.RUnlock()

	result := make([]string, 0, len(contributionsByExtension))
	for _, extensionID := range slices.Sorted(maps.Keys(contributionsByExtension)) {
		entries := contributionsByExtension[extensionID]
		slices.SortFunc(entries, compareFollowUpContributions)

		var followUp string
		for _, entry := range entries {
			followUp = strings.TrimSpace(entry.text)
		}
		if followUp != "" {
			result = append(result, followUp)
		}
	}

	return strings.Join(result, "\n\n")
}

func compareFollowUpContributions(
	left followUpContribution,
	right followUpContribution,
) int {
	if result := cmp.Compare(
		followUpEventRank(left.eventName),
		followUpEventRank(right.eventName),
	); result != 0 {
		return result
	}
	if result := cmp.Compare(left.eventName, right.eventName); result != 0 {
		return result
	}
	return cmp.Compare(left.instanceID, right.instanceID)
}

func followUpEventRank(eventName string) int {
	switch eventName {
	case "postrestore":
		return 0
	case "postbuild":
		return 1
	case "postpackage":
		return 2
	case "postprovision":
		return 3
	case "postpublish":
		return 4
	case "postdeploy":
		return 5
	default:
		return 6
	}
}
