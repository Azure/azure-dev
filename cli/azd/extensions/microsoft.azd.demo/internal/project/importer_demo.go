// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Ensure DemoImporterProvider implements ImporterProvider interface
var _ azdext.ImporterProvider = &DemoImporterProvider{}

const (
	// formatHeader is the front-matter value that identifies azd-infra-gen resource files.
	formatHeader = "azd-infra-gen/v1"
)

// DemoImporterProvider demonstrates how to build an extension importer.
//
// It detects projects that contain .md files with a "azd-infra-gen/v1" front-matter header,
// parses resource definitions from them, and generates Bicep infrastructure.
//
// This shows extension authors how to:
//   - Detect a project type via CanImport
//   - Extract services via Services
//   - Generate temporary infrastructure for `azd provision` via ProjectInfrastructure
//   - Generate permanent infrastructure for `azd infra gen` via GenerateAllInfrastructure
type DemoImporterProvider struct {
	azdClient *azdext.AzdClient
}

// NewDemoImporterProvider creates a new DemoImporterProvider instance
func NewDemoImporterProvider(azdClient *azdext.AzdClient) azdext.ImporterProvider {
	return &DemoImporterProvider{
		azdClient: azdClient,
	}
}

// resourceDef represents a parsed resource from the markdown file.
type resourceDef struct {
	Title    string
	Type     string
	Location string
	Name     string
	Kind     string
	Sku      string
	Tags     map[string]string
}

// CanImport checks if any .md file in the service path has the azd-infra-gen/v1 front-matter.
func (p *DemoImporterProvider) CanImport(
	ctx context.Context,
	svcConfig *azdext.ServiceConfig,
) (bool, error) {
	_, err := p.findInfraGenFiles(svcConfig.RelativePath)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// Services returns the original service as-is. The demo importer focuses on infrastructure
// generation, not service extraction.
func (p *DemoImporterProvider) Services(
	ctx context.Context,
	projectConfig *azdext.ProjectConfig,
	svcConfig *azdext.ServiceConfig,
) (map[string]*azdext.ServiceConfig, error) {
	return map[string]*azdext.ServiceConfig{
		svcConfig.Name: svcConfig,
	}, nil
}

// defaultImporterDir is the default directory name where the demo importer looks for resource files.
// Extensions control their defaults — users can override via infra.importer.options.path in azure.yaml.
const defaultImporterDir = "demo-importer"

// mainParametersJSON is the standard azd parameters file that maps environment variables
// to Bicep parameters. In a real importer, this would be dynamically generated based on
// the parameters discovered in the resource definitions.
const mainParametersJSON = `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "environmentName": {
      "value": "${AZURE_ENV_NAME}"
    },
    "location": {
      "value": "${AZURE_LOCATION}"
    },
    "principalId": {
      "value": "${AZURE_PRINCIPAL_ID}"
    }
  }
}
`

// resolvePath determines the directory containing resource definition files.
// It checks the "path" option first, falling back to the default "demo-importer" directory.
func resolvePath(projectPath string, options map[string]string) string {
	dir := defaultImporterDir
	if v, ok := options["path"]; ok && v != "" {
		dir = v
	}
	return filepath.Join(projectPath, dir)
}

// ProjectInfrastructure generates temporary Bicep infrastructure for `azd provision`.
func (p *DemoImporterProvider) ProjectInfrastructure(
	ctx context.Context,
	projectPath string,
	options map[string]string,
	progress azdext.ProgressReporter,
) (*azdext.ImporterProjectInfrastructureResponse, error) {
	importerDir := resolvePath(projectPath, options)
	progress(fmt.Sprintf("Scanning %s for azd-infra-gen resource definitions...", importerDir))

	resources, err := p.parseAllResources(importerDir)
	if err != nil {
		return nil, fmt.Errorf("parsing resource definitions: %w", err)
	}

	progress(fmt.Sprintf("Generating Bicep for %d resources...", len(resources)))

	mainBicep := generateBicep(resources)
	files := []*azdext.GeneratedFile{
		{
			Path:    "main.bicep",
			Content: []byte(mainBicep),
		},
		{
			Path:    "main.parameters.json",
			Content: []byte(mainParametersJSON),
		},
	}

	if resBicep := generateResourcesBicep(resources); resBicep != "" {
		files = append(files, &azdext.GeneratedFile{
			Path:    "resources.bicep",
			Content: []byte(resBicep),
		})
	}

	return &azdext.ImporterProjectInfrastructureResponse{
		InfraOptions: &azdext.InfraOptions{
			Provider: "bicep",
			Module:   "main",
		},
		Files: files,
	}, nil
}

// GenerateAllInfrastructure generates the complete infrastructure for `azd infra gen`.
func (p *DemoImporterProvider) GenerateAllInfrastructure(
	ctx context.Context,
	projectPath string,
	options map[string]string,
) ([]*azdext.GeneratedFile, error) {
	importerDir := resolvePath(projectPath, options)
	resources, err := p.parseAllResources(importerDir)
	if err != nil {
		return nil, fmt.Errorf("parsing resource definitions: %w", err)
	}

	mainBicep := generateBicep(resources)
	files := []*azdext.GeneratedFile{
		{
			Path:    "infra/main.bicep",
			Content: []byte(mainBicep),
		},
		{
			Path:    "infra/main.parameters.json",
			Content: []byte(mainParametersJSON),
		},
	}

	if resBicep := generateResourcesBicep(resources); resBicep != "" {
		files = append(files, &azdext.GeneratedFile{
			Path:    "infra/resources.bicep",
			Content: []byte(resBicep),
		})
	}

	return files, nil
}

// findInfraGenFiles returns paths of .md files with the azd-infra-gen/v1 header.
func (p *DemoImporterProvider) findInfraGenFiles(basePath string) ([]string, error) {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(basePath, entry.Name())
		if hasInfraGenHeader(filePath) {
			files = append(files, filePath)
		}
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no azd-infra-gen/v1 files found in %s", basePath)
	}

	return files, nil
}

// hasInfraGenHeader checks if a file starts with the azd-infra-gen/v1 front-matter.
func hasInfraGenHeader(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	// First line must be "---"
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return false
	}

	// Scan front-matter lines looking for format: azd-infra-gen/v1
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			break // end of front-matter
		}
		if strings.HasPrefix(line, "format:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "format:"))
			return value == formatHeader
		}
	}

	return false
}

// parseAllResources reads all infra-gen .md files and extracts resource definitions.
func (p *DemoImporterProvider) parseAllResources(basePath string) ([]resourceDef, error) {
	files, err := p.findInfraGenFiles(basePath)
	if err != nil {
		return nil, err
	}

	var resources []resourceDef
	for _, file := range files {
		parsed, err := parseResourceFile(file)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", file, err)
		}
		resources = append(resources, parsed...)
	}

	return resources, nil
}

// parseResourceFile extracts resource definitions from a single .md file.
// Each H1 heading starts a new resource. Properties are parsed from "- key: value" lines.
func parseResourceFile(path string) ([]resourceDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var resources []resourceDef
	var current *resourceDef
	inFrontMatter := false
	parsingTags := false

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)

		// Skip front-matter
		if trimmed == "---" {
			inFrontMatter = !inFrontMatter
			continue
		}
		if inFrontMatter {
			continue
		}

		// H1 heading starts a new resource
		if strings.HasPrefix(trimmed, "# ") {
			if current != nil {
				resources = append(resources, *current)
			}
			current = &resourceDef{
				Title: strings.TrimPrefix(trimmed, "# "),
				Tags:  make(map[string]string),
			}
			parsingTags = false
			continue
		}

		if current == nil {
			continue
		}

		// Parse "- key: value" properties
		if strings.HasPrefix(trimmed, "- ") {
			prop := strings.TrimPrefix(trimmed, "- ")

			// Check for tag entries (indented under tags:)
			if parsingTags {
				if strings.Contains(prop, ":") {
					parts := strings.SplitN(prop, ":", 2)
					current.Tags[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
					continue
				}
			}

			if strings.HasPrefix(prop, "tags:") {
				parsingTags = true
				continue
			}

			if strings.Contains(prop, ":") {
				parts := strings.SplitN(prop, ":", 2)
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])

				switch key {
				case "type":
					current.Type = value
				case "location":
					current.Location = value
				case "name":
					current.Name = value
				case "kind":
					current.Kind = value
				case "sku":
					current.Sku = value
				}
			}

			parsingTags = false
		}
	}

	if current != nil {
		resources = append(resources, *current)
	}

	return resources, nil
}

// generateBicep creates a Bicep template from parsed resource definitions.
func generateBicep(resources []resourceDef) string {
	var b strings.Builder

	b.WriteString("targetScope = 'subscription'\n\n")
	b.WriteString("@minLength(1)\n")
	b.WriteString("@maxLength(64)\n")
	b.WriteString("@description('Name of the environment')\n")
	b.WriteString("param environmentName string\n\n")
	b.WriteString("@description('Primary location for all resources')\n")
	b.WriteString("param location string\n\n")

	// Track if we have a resource group so other resources can reference it
	hasResourceGroup := false
	rgVarName := ""

	for _, res := range resources {
		switch res.Type {
		case "Microsoft.Resources/resourceGroups":
			hasResourceGroup = true
			rgVarName = bicepVarName(res.Title)
			writeResourceGroup(&b, res, rgVarName)
		}
	}

	// Non-resource-group resources go into a module scoped to the resource group
	var nonRGResources []resourceDef
	for _, res := range resources {
		if res.Type != "Microsoft.Resources/resourceGroups" {
			nonRGResources = append(nonRGResources, res)
		}
	}

	if len(nonRGResources) > 0 && hasResourceGroup {
		b.WriteString("\nmodule resources 'resources.bicep' = {\n")
		b.WriteString("  name: 'resources'\n")
		b.WriteString(fmt.Sprintf("  scope: %s\n", rgVarName))
		b.WriteString("  params: {\n")
		b.WriteString("    environmentName: environmentName\n")
		b.WriteString("    location: location\n")
		b.WriteString("  }\n")
		b.WriteString("}\n")
	}

	// Also generate a resources.bicep for non-RG resources
	// (returned as part of the file set)
	return b.String()
}

// generateResourcesBicep creates the resources.bicep module for non-RG resources.
func generateResourcesBicep(resources []resourceDef) string {
	var b strings.Builder

	b.WriteString("param environmentName string\n")
	b.WriteString("param location string\n\n")

	for _, res := range resources {
		if res.Type == "Microsoft.Resources/resourceGroups" {
			continue
		}

		varName := bicepVarName(res.Title)
		name := resolveEnvVars(res.Name)

		switch res.Type {
		case "Microsoft.Storage/storageAccounts":
			sku := res.Sku
			if sku == "" {
				sku = "Standard_LRS"
			}
			kind := res.Kind
			if kind == "" {
				kind = "StorageV2"
			}

			b.WriteString(fmt.Sprintf("resource %s 'Microsoft.Storage/storageAccounts@2023-05-01' = {\n", varName))
			b.WriteString(fmt.Sprintf("  name: %s\n", name))
			b.WriteString("  location: location\n")
			b.WriteString(fmt.Sprintf("  kind: '%s'\n", kind))
			b.WriteString("  sku: {\n")
			b.WriteString(fmt.Sprintf("    name: '%s'\n", sku))
			b.WriteString("  }\n")

			if len(res.Tags) > 0 {
				b.WriteString("  tags: {\n")
				for k, v := range res.Tags {
					b.WriteString(fmt.Sprintf("    '%s': %s\n", k, resolveEnvVars(v)))
				}
				b.WriteString("  }\n")
			}

			b.WriteString("}\n\n")

		case "Microsoft.Web/staticSites":
			sku := res.Sku
			if sku == "" {
				sku = "Free"
			}

			b.WriteString(fmt.Sprintf("resource %s 'Microsoft.Web/staticSites@2022-09-01' = {\n", varName))
			b.WriteString(fmt.Sprintf("  name: %s\n", name))
			b.WriteString("  location: location\n")
			b.WriteString("  sku: {\n")
			b.WriteString(fmt.Sprintf("    name: '%s'\n", sku))
			b.WriteString("    tier: 'Free'\n")
			b.WriteString("  }\n")
			b.WriteString("  properties: {}\n")

			if len(res.Tags) > 0 {
				b.WriteString("  tags: {\n")
				for k, v := range res.Tags {
					b.WriteString(fmt.Sprintf("    '%s': %s\n", k, resolveEnvVars(v)))
				}
				b.WriteString("  }\n")
			}

			b.WriteString("}\n\n")

		default:
			// Generic resource placeholder
			b.WriteString(fmt.Sprintf("// TODO: %s (%s) - unsupported resource type\n\n", res.Title, res.Type))
		}
	}

	return b.String()
}

// bicepVarName converts a title to a valid Bicep variable name.
func bicepVarName(title string) string {
	name := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, title)

	if len(name) == 0 {
		return "resource"
	}
	// Lowercase first letter
	return strings.ToLower(name[:1]) + name[1:]
}

// resolveEnvVars converts ${VAR} patterns to Bicep string interpolation or parameter references.
func resolveEnvVars(s string) string {
	paramMap := map[string]string{
		"${AZURE_ENV_NAME}": "environmentName",
		"${AZURE_LOCATION}": "location",
	}

	// Entire string is a single variable reference -> bare parameter name
	for varRef, paramName := range paramMap {
		if s == varRef {
			return paramName
		}
	}

	// Contains variable references mixed with text -> Bicep string interpolation
	hasVar := false
	result := s
	for varRef, paramName := range paramMap {
		if strings.Contains(result, varRef) {
			hasVar = true
			result = strings.ReplaceAll(result, varRef, "${"+paramName+"}")
		}
	}

	if hasVar {
		return "'" + result + "'"
	}

	// Plain string literal
	return fmt.Sprintf("'%s'", s)
}

// writeResourceGroup writes a resource group resource to the Bicep builder.
func writeResourceGroup(b *strings.Builder, res resourceDef, varName string) {
	name := resolveEnvVars(res.Name)

	b.WriteString(fmt.Sprintf("resource %s 'Microsoft.Resources/resourceGroups@2021-04-01' = {\n", varName))
	b.WriteString(fmt.Sprintf("  name: %s\n", name))
	b.WriteString("  location: location\n")

	if len(res.Tags) > 0 {
		b.WriteString("  tags: {\n")
		for k, v := range res.Tags {
			b.WriteString(fmt.Sprintf("    %s: %s\n", k, resolveEnvVars(v)))
		}
		b.WriteString("  }\n")
	}

	b.WriteString("}\n")
}
