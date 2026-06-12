// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import "embed"

// Re-generate main.arm.json from main.bicep. Run via `go generate ./...`
// from the extension root after editing any *.bicep file under templates/.
// The compiled ARM JSON is embedded so the provisioning provider never
// needs a bicep CLI at user runtime.
//
//go:generate bicep build templates/main.bicep --outfile templates/main.arm.json

//go:embed templates/main.bicep
//go:embed templates/main.arm.json
//go:embed templates/abbreviations.json
//go:embed templates/modules/*.bicep
var templatesFS embed.FS

// TemplatesFS exposes the embedded provisioning templates. Callers that
// only need the ready-to-deploy ARM JSON should prefer ARMTemplate().
func TemplatesFS() embed.FS { return templatesFS }

// ARMTemplate returns the compiled ARM JSON for main.bicep.
func ARMTemplate() ([]byte, error) {
	return templatesFS.ReadFile("templates/main.arm.json")
}
