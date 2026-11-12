// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

// Re-generate the shared Bicep sources and ARM JSON. Run via `go generate`
// from this module after editing any *.bicep or *.bicep.tmpl file under templates/.
// Deployment uses embedded ARM JSON. Preflight may use azd-managed Bicep for
// constant name expressions in the actual compiled plan, including plans
// that reference supplied resources.
//
//go:generate go run ./internal/generatebicep
//go:generate bicep build templates/main.bicep --outfile templates/main.arm.json
//go:generate bicep build templates/existing-project.bicep --outfile templates/existing-project.arm.json

//go:embed templates/main.bicep
//go:embed templates/main.bicep.tmpl
//go:embed templates/main.arm.json
//go:embed templates/existing-project.bicep
//go:embed templates/existing-project-eject.bicep.tmpl
//go:embed templates/existing-project.arm.json
//go:embed templates/abbreviations.json
//go:embed templates/modules/*.bicep
//go:embed templates/modules/*.bicep.tmpl
var templatesFS embed.FS

// terraformTemplatesFS holds the on-disk Terraform module emitted by
// `azd ai agent init --infra=terraform`. Unlike the Bicep templates there is
// no compile step (no ARM JSON to regenerate); azd-core's built-in Terraform
// provider consumes the .tf files directly at `azd provision`.
//
// container-registry.tf is copied only when an agent uses docker:; outputs.tf is generated
// from outputs.tf.tmpl (text/template) so the ACR outputs reference the
// registry resources only when container-registry.tf is present. main.tfvars.json is likewise
// generated at eject time.
//
//go:embed templates/terraform/*.tf
//go:embed templates/terraform/*.tf.tmpl
var terraformTemplatesFS embed.FS

//go:embed templates/terraform-existing-project/*.tf
//go:embed templates/terraform-existing-project/outputs.tf.tmpl
var existingProjectTerraformTemplatesFS embed.FS

// TemplatesFS exposes the embedded provisioning templates. Callers that
// only need the ready-to-deploy ARM JSON should prefer ARMTemplate().
func TemplatesFS() embed.FS { return templatesFS }

// RenderBicepTemplate renders a shared Bicep source with optional existing-AKS hosting sections.
// The provider's checked-in sources use byoAks=true; eject selects it from the synthesized hosting configuration.
func RenderBicepTemplate(name string, byoAks bool) ([]byte, error) {
	raw, err := templatesFS.ReadFile(name + ".tmpl")
	if err != nil {
		return nil, fmt.Errorf("read Bicep template %s: %w", name, err)
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse Bicep template %s: %w", name, err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, struct{ ByoAks bool }{ByoAks: byoAks}); err != nil {
		return nil, fmt.Errorf("render Bicep template %s: %w", name, err)
	}
	return output.Bytes(), nil
}

// TerraformTemplatesFS exposes the embedded Terraform module (the *.tf files
// and outputs.tf.tmpl under templates/terraform/). The eject step copies the
// static .tf files to ./infra/, renders outputs.tf from outputs.tf.tmpl, and
// generates main.tfvars.json alongside them.
func TerraformTemplatesFS() embed.FS { return terraformTemplatesFS }

// ExistingProjectTerraformTemplatesFS exposes the Terraform module for an existing project.
func ExistingProjectTerraformTemplatesFS() embed.FS { return existingProjectTerraformTemplatesFS }

// ARMTemplate returns the compiled ARM JSON for main.bicep.
func ARMTemplate() ([]byte, error) {
	return templatesFS.ReadFile("templates/main.arm.json")
}

// ExistingProjectARMTemplate returns the compiled editable existing-project graph.
func ExistingProjectARMTemplate() ([]byte, error) {
	return templatesFS.ReadFile("templates/existing-project.arm.json")
}
