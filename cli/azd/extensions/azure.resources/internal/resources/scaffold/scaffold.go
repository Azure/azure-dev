// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"text/template"
)

//go:embed assets/scaffold/templates/* assets/scaffold/base/abbreviations.json assets/scaffold/base/modules/*
var assets embed.FS

const (
	baseRoot     = "assets/scaffold/base"
	templateRoot = "assets/scaffold/templates"
)

// ExecInfraFs renders an infrastructure specification into owned files.
func ExecInfraFs(spec InfraSpec) (map[string][]byte, error) {
	functions := template.FuncMap{
		"bicepName":        BicepName,
		"containerAppName": ContainerAppName,
		"upper":            strings.ToUpper,
		"lower":            strings.ToLower,
		"alphaSnakeUpper":  AlphaSnakeUpper,
		"formatParam":      FormatParameter,
		"bicepString":      BicepString,
		"secretName":       SecretName,
		"hasACA":           HasACA,
		"hasAppService":    HasAppService,
		"isACA":            IsACA,
		"isAppService":     IsAppService,
		"entryEnvKey":      EntryEnvKey,
	}

	t, err := template.New("templates").
		Option("missingkey=error").
		Funcs(functions).
		ParseFS(assets, path.Join(templateRoot, "*"))
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	preExecExpand(&spec)
	if err := validateParameterNames(spec.Parameters); err != nil {
		return nil, err
	}
	files := make(map[string][]byte)
	for _, file := range supportingFiles(spec) {
		source, err := fs.ReadFile(assets, path.Join(baseRoot, file))
		if err != nil {
			return nil, fmt.Errorf("reading scaffold asset %q: %w", file, err)
		}
		files[file] = source
	}

	for _, name := range []string{"main.bicep", "resources.bicep", "main.parameters.json"} {
		var output bytes.Buffer
		if err := t.ExecuteTemplate(&output, name, spec); err != nil {
			return nil, fmt.Errorf("executing %s template: %w", name, err)
		}
		files[name] = output.Bytes()
	}
	if spec.AiFoundryProject != nil {
		var output bytes.Buffer
		if err := t.ExecuteTemplate(&output, "ai-project.bicep", spec); err != nil {
			return nil, fmt.Errorf("executing ai-project.bicep template: %w", err)
		}
		files["ai-project.bicep"] = output.Bytes()
	}
	return files, nil
}

func validateParameterNames(parameters []Parameter) error {
	reserved := map[string]string{
		"environmentname":          "main.bicep",
		"location":                 "main.bicep and resources.bicep",
		"aideploymentslocation":    "main.bicep",
		"principalid":              "main.bicep and resources.bicep",
		"principaltype":            "main.bicep and resources.bicep",
		"tags":                     "resources.bicep",
		"aifoundryprojectendpoint": "resources.bicep",
	}
	seen := make(map[string]string, len(parameters))
	for _, parameter := range parameters {
		key := strings.ToLower(parameter.Name)
		if source, exists := reserved[key]; exists {
			return fmt.Errorf("parameter name %q collides with a generated parameter in %s",
				parameter.Name, source)
		}
		if previous, exists := seen[key]; exists {
			return fmt.Errorf("parameter names %q and %q produce the same Bicep identifier",
				previous, parameter.Name)
		}
		seen[key] = parameter.Name
	}
	return nil
}

func supportingFiles(spec InfraSpec) []string {
	files := []string{"abbreviations.json"}
	if HasACA(spec.Services) {
		files = append(files, "modules/fetch-container-image.bicep")
	}
	if len(spec.Existing) > 0 {
		files = append(files, "modules/role-assignment.bicep", "modules/role-assignment.json")
	}
	if spec.AiFoundryProject != nil && spec.AISearch != nil {
		files = append(files, "modules/ai-search-conn.bicep")
	}
	return files
}

func preExecExpand(spec *InfraSpec) {
	if spec.LayerName != "" {
		spec.Parameters = append(spec.Parameters, Parameter{
			Name:  "layerName",
			Value: spec.LayerName,
			Type:  "string",
		})
	}
	for _, service := range spec.Services {
		if service.Host == ContainerAppKind {
			spec.Parameters = append(spec.Parameters, containerAppExistsParameter(service.Name))
		}
	}
	for _, resource := range spec.Existing {
		spec.Parameters = append(spec.Parameters, Parameter{
			Name:  resource.Name + "Id",
			Value: fmt.Sprintf("${%s}", EntryEnvKey(spec.LayerName, resource.ResourceIdEnvVar)),
			Type:  "string",
		})
	}
}
