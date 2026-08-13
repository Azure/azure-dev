// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"sync"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// StateCache stores one assembled state snapshot for a Doctor run.
type StateCache struct {
	once  sync.Once
	state *nextstep.State
	errs  []error
}

// NewStateCache creates a cache for one Doctor invocation.
func NewStateCache() *StateCache {
	return &StateCache{}
}

// AssembleAgentState returns the cached state or assembles it once.
func (deps Dependencies) AssembleAgentState(ctx context.Context) (*nextstep.State, []error) {
	cache := deps.StateCache
	if cache == nil {
		cache = NewStateCache()
	}

	cache.once.Do(func() {
		assembler := deps.assembleState
		if assembler == nil {
			assembler = func(
				c context.Context,
				client *azdext.AzdClient,
			) (*nextstep.State, []error) {
				return nextstep.AssembleState(c, client)
			}
		}
		cache.state, cache.errs = assembler(ctx, deps.AzdClient)
	})

	return cache.state, cache.errs
}
