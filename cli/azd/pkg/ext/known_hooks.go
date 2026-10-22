// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"go.opentelemetry.io/otel/attribute"
)

// knownHookNames is the set of built-in azd lifecycle hook names.
// Unknown names are omitted from telemetry, even when the runtime accepts them.
//
// It is unexported and accessed only through IsKnownHookName / HookNameAttribute
// so the allowlist cannot be mutated from outside the package — an external
// mutation (e.g. adding a user-provided name) could cause raw `hooks.name`
// emission and a privacy regression.
//
// See https://github.com/Azure/azure-dev/issues/7348 for tracking.
var knownHookNames = map[string]bool{
	"prebuild":        true,
	"postbuild":       true,
	"predeploy":       true,
	"postdeploy":      true,
	"predown":         true,
	"postdown":        true,
	"preinfracreate":  true,
	"postinfracreate": true,
	"preinfradelete":  true,
	"postinfradelete": true,
	"prepackage":      true,
	"postpackage":     true,
	"preprovision":    true,
	"postprovision":   true,
	"prepublish":      true,
	"postpublish":     true,
	"prerestore":      true,
	"postrestore":     true,
	"preup":           true,
	"postup":          true,
}

// IsKnownHookName reports whether name is one of the built-in azd lifecycle
// hook names, including the schema-defined infra command aliases.
func IsKnownHookName(name string) bool {
	return knownHookNames[name]
}

// HookNameAttribute returns a telemetry attribute only for a known lifecycle name.
// Callers must omit the attribute when the second result is false.
func HookNameAttribute(name string) (attribute.KeyValue, bool) {
	if IsKnownHookName(name) {
		return fields.HooksNameKey.String(name), true
	}
	var omitted attribute.KeyValue
	return omitted, false
}
