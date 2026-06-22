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

// terraformTemplatesFS holds the on-disk Terraform module emitted by
// `azd ai agent init --infra=terraform`. Unlike the Bicep templates there is
// no compile step (no ARM JSON to regenerate); azd-core's built-in Terraform
// provider consumes the .tf files directly at `azd provision`.
//
//go:embed templates/terraform/*.tf
var terraformTemplatesFS embed.FS

// TemplatesFS exposes the embedded provisioning templates. Callers that
// only need the ready-to-deploy ARM JSON should prefer ARMTemplate().
func TemplatesFS() embed.FS { return templatesFS }

// TerraformTemplatesFS exposes the embedded Terraform module (the *.tf files
// under templates/terraform/). The eject step copies these to ./infra/ and
// generates main.tfvars.json alongside them.
func TerraformTemplatesFS() embed.FS { return terraformTemplatesFS }

// ARMTemplate returns the compiled ARM JSON for main.bicep.
func ARMTemplate() ([]byte, error) {
	return templatesFS.ReadFile("templates/main.arm.json")
}
