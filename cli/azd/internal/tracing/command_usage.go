// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tracing

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
)

// commandUsage is the process-global command usage scope stack. It mirrors the
// package-global usageVal store: a single process-wide instance reachable from
// the command middleware and from any gRPC handler goroutine without dependency
// injection. Extension-contributed command usage attributes are held here and
// attached to the owning command span when its scope closes.
var commandUsage = commandUsageStore{}

type commandUsageStore struct {
	mu     sync.Mutex
	nextID uint64
	scopes []*commandUsageScope
}

type commandUsageScope struct {
	id        uint64
	eventName string
	values    map[attribute.Key]map[string]struct{}
}

// CommandUsageScope is an opaque handle to a pushed command usage scope. The
// command middleware holds it between BeginCommandUsageScope and
// CloseCommandUsageScope.
type CommandUsageScope struct {
	id uint64
}

// BeginCommandUsageScope pushes a new command usage scope for eventName and
// returns its handle. Every command telemetry middleware invocation begins
// exactly one scope, including nested child actions, so the top of the stack
// always identifies the command that owns any values reported while it runs.
func BeginCommandUsageScope(eventName string) CommandUsageScope {
	commandUsage.mu.Lock()
	defer commandUsage.mu.Unlock()

	commandUsage.nextID++
	id := commandUsage.nextID
	commandUsage.scopes = append(commandUsage.scopes, &commandUsageScope{
		id:        id,
		eventName: eventName,
		values:    map[attribute.Key]map[string]struct{}{},
	})

	return CommandUsageScope{id: id}
}

// TryAppendCommandUsageUnique appends value to the current (top) scope when its
// exact event name is present in eligibleEvents. It returns false when no scope
// is active or the current scope is not eligible. It never falls back to an
// ancestor scope, so a value reported while an ineligible command (for example
// a synthetic child cmd.package) is on top is discarded rather than attributed
// to a parent command.
func TryAppendCommandUsageUnique(
	eligibleEvents map[string]struct{},
	key attribute.Key,
	value string,
) bool {
	commandUsage.mu.Lock()
	defer commandUsage.mu.Unlock()

	if len(commandUsage.scopes) == 0 {
		return false
	}

	top := commandUsage.scopes[len(commandUsage.scopes)-1]
	if _, ok := eligibleEvents[top.eventName]; !ok {
		return false
	}

	set, ok := top.values[key]
	if !ok {
		set = map[string]struct{}{}
		top.values[key] = set
	}
	set[value] = struct{}{}

	return true
}

// CloseCommandUsageScope pops the scope identified by scope and returns its
// accumulated attributes as deterministically sorted string slices. It returns
// an error when the handle does not identify the current top scope, which
// covers a repeated or out-of-order close. On error the stack is left
// unchanged so a balanced close by the true owner still succeeds.
func CloseCommandUsageScope(scope CommandUsageScope) ([]attribute.KeyValue, error) {
	commandUsage.mu.Lock()
	defer commandUsage.mu.Unlock()

	n := len(commandUsage.scopes)
	if n == 0 {
		return nil, fmt.Errorf("no active command usage scope to close")
	}

	top := commandUsage.scopes[n-1]
	if top.id != scope.id {
		return nil, fmt.Errorf(
			"command usage scope is not the current scope; expected %d, got %d",
			top.id, scope.id,
		)
	}

	commandUsage.scopes = commandUsage.scopes[:n-1]

	keys := make([]attribute.Key, 0, len(top.values))
	for k := range top.values {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b attribute.Key) int {
		return strings.Compare(string(a), string(b))
	})

	attrs := make([]attribute.KeyValue, 0, len(keys))
	for _, k := range keys {
		valuesSet := top.values[k]
		values := make([]string, 0, len(valuesSet))
		for v := range valuesSet {
			values = append(values, v)
		}
		slices.Sort(values)
		attrs = append(attrs, k.StringSlice(values))
	}

	return attrs, nil
}

// ResetCommandUsageForTest clears all command usage scopes. Command usage state
// is process-global; tests that rely on it must not run with t.Parallel().
func ResetCommandUsageForTest() {
	commandUsage.mu.Lock()
	defer commandUsage.mu.Unlock()

	commandUsage.scopes = nil
	commandUsage.nextID = 0
}
