// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package ai_agents_test

import (
	"context"
	"testing"
)

func newTestContext(t *testing.T) (context.Context, context.CancelFunc) {
	ctx := t.Context()

	if deadline, ok := t.Deadline(); ok {
		return context.WithDeadline(ctx, deadline)
	}

	return context.WithCancel(ctx)
}

// tempDirWithDiagnostics returns t.TempDir(). On Linux CI (where these tests run),
// no additional diagnostics are needed. This keeps the package self-contained
// without pulling Windows-specific cleanup logic from the parent package.
func tempDirWithDiagnostics(t *testing.T) string {
	return t.TempDir()
}
