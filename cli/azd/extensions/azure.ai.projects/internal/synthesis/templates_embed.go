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
//go:generate bicep build templates/brownfield.bicep --outfile templates/brownfield.arm.json

//go:embed templates/main.bicep
//go:embed templates/main.arm.json
//go:embed templates/brownfield.bicep
//go:embed templates/brownfield.arm.json
//go:embed templates/abbreviations.json
//go:embed templates/modules/*.bicep
var templatesFS embed.FS

// terraformTemplatesFS holds the on-disk Terraform module emitted by
// `azd ai agent init --infra=terraform`. Unlike the Bicep templates there is
// no compile step (no ARM JSON to regenerate); azd-core's built-in Terraform
// provider consumes the .tf files directly at `azd provision`.
//
// acr.tf is copied only when an agent uses docker:; outputs.tf is generated
// from outputs.tf.tmpl (text/template) so the ACR outputs reference the
// registry resources only when acr.tf is present. main.tfvars.json is likewise
// generated at eject time.
//
//go:embed templates/terraform/*.tf
//go:embed templates/terraform/outputs.tf.tmpl
var terraformTemplatesFS embed.FS

// TemplatesFS exposes the embedded provisioning templates. Callers that
// only need the ready-to-deploy ARM JSON should prefer ARMTemplate().
func TemplatesFS() embed.FS { return templatesFS }

// TerraformTemplatesFS exposes the embedded Terraform module (the *.tf files
// and outputs.tf.tmpl under templates/terraform/). The eject step copies the
// static .tf files to ./infra/, renders outputs.tf from outputs.tf.tmpl, and
// generates main.tfvars.json alongside them.
func TerraformTemplatesFS() embed.FS { return terraformTemplatesFS }

// ARMTemplate returns the compiled ARM JSON for main.bicep.
func ARMTemplate() ([]byte, error) {
	return templatesFS.ReadFile("templates/main.arm.json")
}

// BrownfieldARMTemplate returns the compiled ARM JSON for brownfield.bicep, which
// creates/upserts model deployments on an EXISTING Foundry account (referenced,
// not created). Used by the provider when the project sets endpoint: and declares
// deployments: to add to the existing project.
func BrownfieldARMTemplate() ([]byte, error) {
	return templatesFS.ReadFile("templates/brownfield.arm.json")
}
