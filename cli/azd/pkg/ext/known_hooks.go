// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"go.opentelemetry.io/otel/attribute"
)

// knownHookNames is the set of built-in azd lifecycle hook names.
// Extension-defined hooks (and any user-defined names) are NOT included here;
// those are hashed before being emitted in telemetry to avoid leaking
// user-chosen identifiers via the `hooks.name` attribute.
//
// It is unexported and accessed only through IsKnownHookName / HookNameAttribute
// so the allowlist cannot be mutated from outside the package — an external
// mutation (e.g. adding a user-provided name) could cause raw `hooks.name`
// emission and a privacy regression.
//
// See https://github.com/Azure/azure-dev/issues/7348 for tracking.
var knownHookNames = map[string]bool{
	"prebuild":      true,
	"postbuild":     true,
	"predeploy":     true,
	"postdeploy":    true,
	"predown":       true,
	"postdown":      true,
	"prepackage":    true,
	"postpackage":   true,
	"preprovision":  true,
	"postprovision": true,
	"prepublish":    true,
	"postpublish":   true,
	"prerestore":    true,
	"postrestore":   true,
	"preup":         true,
	"postup":        true,
}

// IsKnownHookName reports whether name is one of the built-in azd lifecycle
// hook names. Extension- or project-author-defined names return false and are
// hashed before emission in telemetry.
func IsKnownHookName(name string) bool {
	return knownHookNames[name]
}

// HookNameAttribute returns the telemetry attribute for the `hooks.name` field.
// Built-in lifecycle hook names are emitted raw; any other (user- or
// extension-defined) name is hashed via fields.StringHashed so identifiers are
// not leaked. The allowlist check happens before hashing so the common path
// does no unnecessary SHA-256 work.
func HookNameAttribute(name string) attribute.KeyValue {
	if IsKnownHookName(name) {
		return fields.HooksNameKey.String(name)
	}
	return fields.StringHashed(fields.HooksNameKey, name)
}
