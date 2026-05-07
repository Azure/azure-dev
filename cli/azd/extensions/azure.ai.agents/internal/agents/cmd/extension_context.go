// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

// ensureExtensionContext returns a non-nil [azdext.ExtensionContext] so command
// constructors can be safely invoked from tests with a nil receiver. The SDK's
// [azdext.NewExtensionRootCommand] populates the real context (and its env-var
// fallback) before any leaf RunE runs, so tests that don't exercise RunE can
// safely pass nil here.
func ensureExtensionContext(extCtx *azdext.ExtensionContext) *azdext.ExtensionContext {
	if extCtx == nil {
		return &azdext.ExtensionContext{}
	}
	return extCtx
}
