// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// EvalScopeSuffix marks which configuration owns an unqualified state key.
const EvalScopeSuffix = "_SCOPE"

// EvalScope identifies the configuration a recorded eval id belongs to.
//
// Ids are recorded per environment under the eval's declared name. One
// `azure.yaml` can carry several `azure.ai.eval` services, and two agents each
// with an eval called `quality` is an ordinary layout -- so the name alone does
// not say whose id it is.
//
// Relative to the project root rather than absolute, so moving or cloning the
// tree still reads as the same configuration and the ids recorded for it stay
// reachable.
//
// Case is folded only where the filesystem folds it. Folding everywhere made
// `evals/A/azure.eval.yaml` and `evals/a/azure.eval.yaml` -- two files on Linux
// -- one scope, which is the collision this exists to prevent. The opposite
// mistake is the safe one: a configuration reached by an unexpected spelling
// records nothing under it, and the lookup falls through to asking the service
// by name.
func EvalScope(projectRoot, configPath string) string {
	if configPath == "" {
		return ""
	}
	rel := configPath
	if projectRoot != "" {
		if r, err := filepath.Rel(projectRoot, configPath); err == nil {
			rel = r
		}
	}
	rel = filepath.ToSlash(rel)
	if pathsAreCaseInsensitive {
		rel = strings.ToLower(rel)
	}
	return rel
}

// pathsAreCaseInsensitive reports whether two spellings of a path name one file.
//
// Windows only. macOS is usually case insensitive too but can be formatted
// either way, and guessing wrong there costs a collision rather than a missed
// lookup.
var pathsAreCaseInsensitive = runtime.GOOS == "windows"

// EvalScopeTag is the part of a state key that names a scope, short enough to
// leave the key readable.
func EvalScopeTag(scope string) string {
	sum := sha256.Sum256([]byte(scope))
	return strings.ToUpper(hex.EncodeToString(sum[:4]))
}

// EvalScopeOfService is the scope of the configuration a service points at.
//
// Derived from the `$ref` rather than from the service name, because that is
// what the lookup side has: `run` is given a directory, not a service.
//
// The `$ref` is used as written, not reduced to its directory. A `$ref` names a
// file by name, so `./config/nightly.yaml` reduced to `./config` came back as
// `./config/azure.eval.yaml` -- a scope naming a file that is not the one being
// deployed, and not the one the lookup side computes from the same project. Ids
// recorded under it were never found again, and the next deploy made a second
// eval.
func EvalScopeOfService(svc *azdext.ServiceConfig, projectRoot string) string {
	if svc == nil {
		return ""
	}
	location := serviceRelativeConfig(svc)
	if location == "" || location == "." {
		return ""
	}
	return EvalScope(projectRoot, resolvedConfigPath(filepath.Join(projectRoot, location)))
}

// serviceRelativeConfig is the configuration a service points at, as written.
//
// serviceRelativeDir answers the neighboring question -- which directory the
// service's relative paths resolve against -- and throws the filename away to
// do it.
func serviceRelativeConfig(svc *azdext.ServiceConfig) string {
	if props := serviceProps(svc); props != nil {
		if ref, ok := props.AsMap()["$ref"].(string); ok && ref != "" {
			return filepath.FromSlash(ref)
		}
	}
	return serviceRelativeDir(svc)
}
