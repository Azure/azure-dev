// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type buildGateContextKey struct{}

// ContextWithBuildGate returns a new context carrying the given mutex as a
// build gate. Package, publish, and deploy operations that perform
// concurrent-unsafe builds use this as a coordination token for the local
// build subprocess and release any acquired lock before Azure operations.
//
// This is a FALLBACK mechanism. The preferred approach is to use
// [dotnet.ContextWithArtifactsPath] to isolate intermediate outputs, which
// allows full parallelism without acquiring the mutex.
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

func runIsolatedDotNetBuild(
	ctx context.Context,
	serviceName string,
	dotNetCli *dotnet.Cli,
	build func(context.Context) error,
) error {
	return runIsolatedDotNetBuildWithTempDir(ctx, serviceName, dotNetCli, os.MkdirTemp, build)
}

func runIsolatedDotNetBuildWithTempDir(
	ctx context.Context,
	serviceName string,
	dotNetCli *dotnet.Cli,
	mkdirTemp func(string, string) (string, error),
	build func(context.Context) error,
) error {
	buildGate := BuildGateFromContext(ctx)
	if buildGate == nil {
		return build(ctx)
	}

	supportsArtifactsPath, err := dotNetCli.SupportsArtifactsPath(ctx)
	if err != nil {
		log.Printf(
			"warning: failed to detect .NET SDK artifacts path support for %s: %v; falling back to serial build",
			serviceName,
			err,
		)
	} else if supportsArtifactsPath {
		safeName := sanitizeTempDirName(serviceName)
		artifactsDir, err := mkdirTemp("", "azd-"+safeName+"-")
		if err == nil {
			defer func() {
				if err := osutil.RemoveAll(context.WithoutCancel(ctx), artifactsDir); err != nil {
					log.Printf("warning: failed to remove artifacts temp dir %s: %v", artifactsDir, err)
				}
			}()

			return build(dotnet.ContextWithArtifactsPath(ctx, artifactsDir))
		}

		log.Printf(
			"warning: failed to create artifacts temp dir for %s: %v; falling back to serial build",
			serviceName,
			err,
		)
	}

	buildGate.Lock()
	defer buildGate.Unlock()

	return build(ctx)
}
