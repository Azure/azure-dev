// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"

	"azureaieval/internal/messages"
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
	return ec.scopedKeyOwnedBy(ctx, base, "", scope)
}

// scopedKeyOwnedBy is scopedKey for a value that is written beside an id. A
// fingerprint recorded before scoping has no owner marker of its own, but the
// id it was recorded with may already be owned by another configuration; in
// that case the unmarked value is that configuration's, not this one's.
func (ec *evalContext) scopedKeyOwnedBy(ctx context.Context, base, ownerBase, scope string) string {
	if scope == "" {
		return base
	}
	owner := ec.privateValue(ctx, base+project.EvalScopeSuffix)
	if owner == "" && ownerBase != "" {
		owner = ec.privateValue(ctx, ownerBase+project.EvalScopeSuffix)
	}
	switch owner {
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

// scopedValueOwnedBy reads what this configuration recorded under a name whose
// unmarked value would follow the ownership of ownerBase.
//
// Before scoping every configuration wrote the same unqualified value, so an
// unmarked one is whichever configuration wrote last, not necessarily the
// owner of the id beside it. Only that legacy value is unknown: for the
// configuration that owns the id it reads as absent, so the next deploy records
// a baseline instead of comparing against another configuration's definition
// and recreating an unchanged eval. A configuration that does not own the id
// has its own suffixed key, and what it recorded there is read as usual.
func (ec *evalContext) scopedValueOwnedBy(ctx context.Context, base, ownerBase, scope string) string {
	if scope != "" && ownerBase != "" && ec.privateValue(ctx, base+project.EvalScopeSuffix) == "" {
		if idOwner := ec.privateValue(ctx, ownerBase+project.EvalScopeSuffix); idOwner != "" {
			if idOwner == scope {
				return ""
			}
			return ec.privateValue(ctx, base+"_"+project.EvalScopeTag(scope))
		}
	}
	return ec.privateValue(ctx, ec.scopedKeyOwnedBy(ctx, base, ownerBase, scope))
}

// rememberScoped records a value under this configuration's key and, the first
// time, which configuration the unqualified key belongs to.
//
// Both go in under one lock, from one read: which key to write depends on who
// owns the unqualified one, so choosing it here and writing it there let two
// concurrent deploys both choose the unqualified key.
func (ec *evalContext) rememberScoped(ctx context.Context, base, scope, value string) {
	ec.rememberScopedOwnedBy(ctx, base, "", scope, value)
}

// rememberScopedOwnedBy is rememberScoped for a value whose unmarked key
// follows the ownership of ownerBase.
func (ec *evalContext) rememberScopedOwnedBy(ctx context.Context, base, ownerBase, scope, value string) {
	err := ec.setPrivateScoped(ctx, base, ownerBase, scope, value)
	if err == nil || errors.Is(err, errNoAzdEnvironment) {
		return
	}
	fmt.Fprint(warnWriter(ctx), messages.Warning(err))
	log.Printf("[env] could not record %s: %v", base, err)
}
