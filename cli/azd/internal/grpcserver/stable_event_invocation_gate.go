// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type stableEventInvocationGate struct {
	mu    sync.Mutex
	slots map[stableEventInvocationKey]*stableEventInvocationSlot
}

type stableEventInvocationKey struct {
	broker        *grpcbroker.MessageBroker[azdext.EventMessage]
	correlationID string
}

type stableEventInvocationSlot struct {
	token chan struct{}
	refs  int
}

func (g *stableEventInvocationGate) run(
	ctx context.Context,
	broker *grpcbroker.MessageBroker[azdext.EventMessage],
	correlationID string,
	action func() error,
) error {
	if err := stableEventContextError(ctx); err != nil {
		return err
	}

	key := stableEventInvocationKey{broker: broker, correlationID: correlationID}
	slot := g.retain(key)
	select {
	case slot.token <- struct{}{}:
		if err := stableEventContextError(ctx); err != nil {
			<-slot.token
			g.release(key, slot)
			return err
		}
	case <-ctx.Done():
		g.release(key, slot)
		return stableEventContextError(ctx)
	}

	defer func() {
		<-slot.token
		g.release(key, slot)
	}()
	return action()
}

func (g *stableEventInvocationGate) retain(key stableEventInvocationKey) *stableEventInvocationSlot {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.slots == nil {
		g.slots = make(map[stableEventInvocationKey]*stableEventInvocationSlot)
	}
	slot := g.slots[key]
	if slot == nil {
		slot = &stableEventInvocationSlot{token: make(chan struct{}, 1)}
		g.slots[key] = slot
	}
	slot.refs++
	return slot
}

func (g *stableEventInvocationGate) release(key stableEventInvocationKey, slot *stableEventInvocationSlot) {
	g.mu.Lock()
	defer g.mu.Unlock()

	slot.refs--
	if slot.refs == 0 && g.slots[key] == slot {
		delete(g.slots, key)
	}
}

func stableEventContextError(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}
