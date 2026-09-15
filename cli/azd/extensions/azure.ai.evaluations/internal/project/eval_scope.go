// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
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
// reachable. Lowercased because the two sides of this can spell the same path
// differently on Windows.
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
	return strings.ToLower(filepath.ToSlash(rel))
}

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
func EvalScopeOfService(svc *azdext.ServiceConfig, projectRoot string) string {
	if svc == nil {
		return ""
	}
	dir := serviceRelativeDir(svc)
	if dir == "" || dir == "." {
		return ""
	}
	return EvalScope(projectRoot, resolvedConfigPath(filepath.Join(projectRoot, dir)))
}
