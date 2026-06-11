// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

// KnownHookNames is the set of built-in azd lifecycle hook names.
// Extension-defined hooks (and any user-defined names) are NOT included here;
// those are hashed before being emitted in telemetry to avoid leaking
// user-chosen identifiers via the `hooks.name` attribute.
//
// Used by the hooks runner (`pkg/ext/hooks_runner.go`) and the
// `azd hooks run` command (`cmd/hooks.go`) to decide whether the hook
// name can be safely emitted raw.
//
// See https://github.com/Azure/azure-dev/issues/7348 for tracking.
var KnownHookNames = map[string]bool{
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
