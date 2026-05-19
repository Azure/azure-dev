// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"strings"
	"sync"
)

type buildGateContextKey struct{}

// ContextWithBuildGate returns a new context carrying the given mutex as a
// build gate. Deploy targets that perform concurrent-unsafe build operations
// (e.g. dotnet publish on Aspire projects that share <ProjectReference>
// dependencies) should acquire this mutex for the duration of the build and
// release it before proceeding to the Azure deployment portion.
//
// This is a FALLBACK mechanism. The preferred approach is to use
// [dotnet.ContextWithArtifactsPath] to isolate intermediate outputs, which
// allows full parallelism without serialization.
func ContextWithBuildGate(ctx context.Context, mu *sync.Mutex) context.Context {
	return context.WithValue(ctx, buildGateContextKey{}, mu)
}

// BuildGateFromContext retrieves the build gate mutex from the context, or nil
// if none was set. Callers should check for nil before locking.
func BuildGateFromContext(ctx context.Context) *sync.Mutex {
	if mu, ok := ctx.Value(buildGateContextKey{}).(*sync.Mutex); ok {
		return mu
	}
	return nil
}

// sanitizeTempDirName replaces characters outside [A-Za-z0-9_-] with
// underscores so the result is safe for use as a temp-directory prefix on all
// platforms.
func sanitizeTempDirName(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
}
