// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"azureaieval/internal/project"
)

// scopedKey is where this configuration's value for a name is recorded.
//
// The first configuration to record one keeps the unqualified key -- the key
// every deploy before scoping wrote -- and a second configuration declaring the
// same name gets its own beside it. Moving an existing project's ids to a new
// key would orphan the evals they point at and split the run history those ids
// exist to keep, which is worse than the collision this prevents.
//
// An unowned key answers as this scope's: nothing recorded who wrote it, so the
// configuration asking is the one that had it.
func (ec *evalContext) scopedKey(ctx context.Context, base, scope string) string {
	if scope == "" {
		return base
	}
	switch ec.privateValue(ctx, base+project.EvalScopeSuffix) {
	case "", scope:
		return base
	default:
		return base + "_" + project.EvalScopeTag(scope)
	}
}

// scopedValue reads what this configuration recorded under a name.
func (ec *evalContext) scopedValue(ctx context.Context, base, scope string) string {
	return ec.privateValue(ctx, ec.scopedKey(ctx, base, scope))
}

// rememberScoped records a value under this configuration's key and, the first
// time, which configuration the unqualified key belongs to.
func (ec *evalContext) rememberScoped(ctx context.Context, base, scope, value string) {
	key := ec.scopedKey(ctx, base, scope)
	ec.remember(ctx, key, value)
	if scope != "" && key == base && ec.privateValue(ctx, base+project.EvalScopeSuffix) == "" {
		ec.remember(ctx, base+project.EvalScopeSuffix, scope)
	}
}
