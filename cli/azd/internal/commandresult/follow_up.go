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

// FollowUpCollector gathers extension follow-up text for one command.
type FollowUpCollector struct {
	mu            sync.RWMutex
	contributions map[string]string
}

// NewFollowUpCollector creates an empty command follow-up collector.
func NewFollowUpCollector() *FollowUpCollector {
	return &FollowUpCollector{
		contributions: make(map[string]string),
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

// Add records follow-up text for an extension.
func (c *FollowUpCollector) Add(extensionID, followUp string) {
	if c == nil || strings.TrimSpace(followUp) == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contributions == nil {
		c.contributions = make(map[string]string)
	}
	c.contributions[extensionID] = followUp
}

// Text returns contributions in deterministic extension ID order.
func (c *FollowUpCollector) Text() string {
	if c == nil {
		return ""
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	contributions := make([]string, 0, len(c.contributions))
	for _, extensionID := range slices.Sorted(maps.Keys(c.contributions)) {
		if followUp := strings.TrimSpace(c.contributions[extensionID]); followUp != "" {
			contributions = append(contributions, followUp)
		}
	}

	return strings.Join(contributions, "\n\n")
}
