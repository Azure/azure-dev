// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package infra ports the host-neutral CU infrastructure workflows.
package infra

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/localio"
)

// Assets are copied from cu-cli-core at 25d39b30116a840491b7b6f393d6db792b38d4a4.
//
//go:embed assets
var assets embed.FS

var environmentPattern = regexp.MustCompile(`^[a-z0-9()_.-]{1,64}$`)
var prefixPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,18}[a-z0-9])?$`)
var assignmentPattern = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)

var resourceParameters = []struct{ name, key, legacy, fallback string }{
	{"foundryResourcePrefix", "CU_FOUNDRY_RESOURCE_PREFIX", "FOUNDRY_RESOURCE_PREFIX", ""},
	{"existingFoundryEndpoint", "CU_FOUNDRY_EXISTING_ENDPOINT", "FOUNDRY_EXISTING_ENDPOINT", ""},
	{"existingFoundryResourceGroup", "CU_FOUNDRY_EXISTING_RESOURCE_GROUP", "FOUNDRY_EXISTING_RESOURCE_GROUP", ""},
	{"assignRolesToPrincipal", "CU_ASSIGN_ROLES", "AZD_ASSIGN_ROLES", "false"},
}

var Regions = []string{
	"australiaeast", "eastus", "eastus2", "japaneast", "northeurope", "southcentralus", "southeastasia",
	"swedencentral", "uksouth", "westeurope", "westus", "westus3",
}

// Choices contains resolved generation inputs; no host state is inferred by the renderer.
type Choices struct {
	Environment             string `json:"environment"`
	Location                string `json:"location"`
	APIVersion              string `json:"api_version"`
	FoundryPrefix           string `json:"foundry_prefix"`
	FoundryEndpoint         string `json:"foundry_endpoint"`
	FoundryResourceGroup    string `json:"foundry_resource_group"`
	ModelSelection          string `json:"model_selection"`
	AssignRoles             bool   `json:"assign_roles"`
	ForceConfigurationSetup bool   `json:"force_configuration_setup"`
	SubscriptionID          string `json:"subscriptionId"`
	SubscriptionName        string `json:"subscriptionName"`
	TenantID                string `json:"tenantId"`
}

// Generation describes only files written by this invocation.
type Generation struct {
	Choices
	OutputDirectory        string   `json:"outputDirectory"`
	Files                  []string `json:"files"`
	ReusedExistingTemplate bool     `json:"reusedExistingTemplate"`
	NextSteps              []string `json:"nextSteps"`
}

func invalid(code, message string) error {
	return &cuclient.Error{Kind: cuclient.ErrorValidation, Code: code, Message: message,
		Suggestion: "Check infra --help and use a valid generated project and explicit noninteractive model selection."}
}

// NormalizeSelection preserves explicit family/version selectors and standalone special modes.
func NormalizeSelection(value string) (string, error) {
	parts := strings.Split(value, ",")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
		if parts[i] == "" {
			return "", invalid("invalid_model_selection", "Model selection contains an empty entry.")
		}
		if slices.Contains([]string{"none", "prompt", "recommended"}, strings.ToLower(parts[i])) {
			if len(parts) != 1 {
				return "", invalid("invalid_model_selection", "Use none, prompt, or recommended alone.")
			}
			parts[i] = strings.ToLower(parts[i])
		}
	}
	return strings.Join(parts, ","), nil
}

// Normalize validates all rendered values before creating files or accessing Azure.
func (c Choices) Normalize() (Choices, error) {
	c.Environment = strings.ToLower(strings.TrimSpace(c.Environment))
	if c.Environment == "." || c.Environment == ".." || !environmentPattern.MatchString(c.Environment) {
		return c, invalid("invalid_environment", "Use a valid azd environment name, not a path.")
	}
	c.Location = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(c.Location)), " ", "")
	if !slices.Contains(Regions, c.Location) {
		return c, invalid("invalid_location", "Select a CU-supported Azure region.")
	}
	c.FoundryPrefix = strings.ToLower(strings.TrimSpace(c.FoundryPrefix))
	if c.FoundryPrefix != "" && !prefixPattern.MatchString(c.FoundryPrefix) {
		return c, invalid("invalid_foundry_prefix", "Use 1-20 lowercase letters, numbers, or internal hyphens.")
	}
	if c.FoundryPrefix != "" && c.FoundryEndpoint != "" {
		return c, invalid("conflicting_foundry_resource", "Specify an existing Foundry endpoint or a new prefix, not both.")
	}
	var err error
	c.APIVersion, err = cuclient.NormalizeAPIVersion(c.APIVersion)
	if err != nil {
		return c, err
	}
	c.ModelSelection, err = NormalizeSelection(c.ModelSelection)
	if err != nil {
		return c, err
	}
	if c.FoundryEndpoint != "" {
		if c.AssignRoles {
			return c, invalid("conflicting_role_assignment",
				"--assign-roles cannot be combined with --foundry-endpoint.")
		}
		c.FoundryEndpoint, err = cuclient.NormalizeEndpoint(c.FoundryEndpoint)
		if err != nil {
			return c, err
		}
		c.FoundryEndpoint = strings.TrimSuffix(c.FoundryEndpoint, "/contentunderstanding")
	}
	for _, value := range []string{
		c.SubscriptionID, c.TenantID, c.FoundryResourceGroup, c.ModelSelection, c.FoundryEndpoint,
	} {
		if strings.ContainsAny(value, "\"\\\r\n`$") {
			return c, invalid("invalid_environment_value", "Generated environment values contain unsafe shell syntax.")
		}
	}
	if c.FoundryEndpoint != "" && c.FoundryResourceGroup == "" {
		return c, invalid("missing_infra_scope", "Generation requires an existing account's resource group.")
	}
	return c, nil
}

func environmentAssignments(c Choices) ([]string, map[string]string) {
	keys := []string{"AZURE_ENV_NAME", "AZURE_LOCATION", "AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID",
		"CU_API_VERSION", "CU_FOUNDRY_RESOURCE_PREFIX", "CU_FOUNDRY_EXISTING_ENDPOINT", "CU_FOUNDRY_EXISTING_RESOURCE_GROUP",
		"CU_ASSIGN_ROLES"}
	values := []string{c.Environment, c.Location, c.SubscriptionID, c.TenantID, c.APIVersion,
		c.FoundryPrefix, c.FoundryEndpoint, c.FoundryResourceGroup, strconv.FormatBool(c.AssignRoles)}
	result := make(map[string]string, len(keys))
	for index, key := range keys {
		result[key] = key + "=\"" + values[index] + "\""
	}
	return keys, result
}

// MergeEnvironment preserves unmanaged lines, including legacy settings, and newline style.
func MergeEnvironment(existing string, c Choices) string {
	keys, assignments := environmentAssignments(c)
	return mergeEnvironment(existing, keys, assignments)
}

func mergeEnvironment(existing string, keys []string, assignments map[string]string) string {
	newline := "\n"
	if strings.Contains(existing, "\r\n") {
		newline = "\r\n"
	}
	seen := map[string]bool{}
	var output strings.Builder
	for line := range strings.SplitAfterSeq(existing, "\n") {
		match := assignmentPattern.FindStringSubmatch(line)
		if len(match) == 0 || assignments[match[1]] == "" {
			output.WriteString(line)
			continue
		}
		key := match[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		ending := newline
		if strings.HasSuffix(line, "\r\n") {
			ending = "\r\n"
		} else if strings.HasSuffix(line, "\n") {
			ending = "\n"
		}
		output.WriteString(assignments[key] + ending)
	}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		if output.Len() > 0 && !strings.HasSuffix(output.String(), "\n") {
			output.WriteString(newline)
		}
		output.WriteString(assignments[key] + newline)
	}
	return output.String()
}

func jsonObject(body []byte) (map[string]json.RawMessage, error) {
	value := map[string]json.RawMessage{}
	if json.Unmarshal(body, &value) != nil || value == nil {
		return nil, invalid("invalid_project_config", "Generated project configuration must be a JSON object.")
	}
	return value, nil
}

func renderParameters(body []byte) ([]byte, map[string]string, error) {
	document, err := jsonObject(body)
	if err != nil {
		return nil, nil, err
	}
	parameters, err := jsonObject(document["parameters"])
	if err != nil {
		return nil, nil, err
	}
	literals := map[string]string{}
	for _, binding := range resourceParameters {
		parameter, err := jsonObject(parameters[binding.name])
		if err != nil {
			return nil, nil, err
		}
		substitution := "${" + binding.key + "=" + binding.fallback + "}"
		legacySubstitution := "${" + binding.legacy + "=" + binding.fallback + "}"
		var value *string
		if json.Unmarshal(parameter["value"], &value) != nil || value == nil ||
			(*value != substitution && *value != legacySubstitution && strings.ContainsAny(*value, "\"\\\r\n`$")) {
			return nil, nil, &cuclient.Error{
				Kind: cuclient.ErrorValidation, Code: "unsupported_resource_parameter",
				Message:    "Resource parameter " + binding.name + " must be a literal string or " + substitution + ".",
				Suggestion: "Review infra/main.parameters.json or generate into a new directory. No files were changed.",
			}
		}
		if *value != substitution && *value != legacySubstitution {
			literals[binding.key] = binding.key + "=\"" + *value + "\""
		}
		parameter["value"], err = json.Marshal(substitution)
		if err != nil {
			return nil, nil, err
		}
		parameters[binding.name], err = json.Marshal(parameter)
		if err != nil {
			return nil, nil, err
		}
	}
	document["parameters"], err = json.Marshal(parameters)
	if err != nil {
		return nil, nil, err
	}
	rendered, err := json.MarshalIndent(document, "", "  ")
	return rendered, literals, err
}

func migrateResourceEnvironments(target, selected string, literals map[string]string) (map[string][]byte, error) {
	writes := map[string][]byte{}
	if _, err := localio.Check(filepath.Join(target, ".azure/config.json")); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(target, ".azure"))
	if errors.Is(err, fs.ErrNotExist) {
		return writes, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Name() == selected {
			continue
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil, invalid("unsafe_project_output", "An azd environment cannot be a symlink during migration.")
		}
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(".azure", entry.Name(), ".env")
		body, err := localio.Read(filepath.Join(target, path))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		values, err := godotenv.Unmarshal(string(body))
		if err != nil {
			return nil, invalid("invalid_environment_value", "A saved azd environment could not be parsed during migration.")
		}
		keys := []string{}
		assignments := map[string]string{}
		for _, binding := range resourceParameters {
			assignment, literal := literals[binding.key]
			if !literal {
				if _, present := values[binding.key]; present {
					continue
				}
				value, present := values[binding.legacy]
				if !present {
					value = binding.fallback
				}
				if strings.ContainsAny(value, "\"\\\r\n`$") {
					return nil, invalid("invalid_environment_value",
						"A saved provisioning input contains unsafe shell syntax.")
				}
				assignment = binding.key + "=\"" + value + "\""
			}
			// Shared literals were effective for every environment, even when saved keys differed.
			keys = append(keys, binding.key)
			assignments[binding.key] = assignment
		}
		if len(keys) != 0 {
			writes[path] = []byte(mergeEnvironment(string(body), keys, assignments))
		}
	}
	return writes, nil
}

func renderEnvironmentConfig(body []byte, c Choices) ([]byte, error) {
	document, err := jsonObject(body)
	if err != nil {
		return nil, err
	}
	section := map[string]json.RawMessage{}
	if raw, present := document["cu"]; present {
		section, err = jsonObject(raw)
		if err != nil {
			return nil, err
		}
	}
	section["modelSelection"], err = json.Marshal(c.ModelSelection)
	if err != nil {
		return nil, err
	}
	section["forceConfigurationSetup"], err = json.Marshal(c.ForceConfigurationSetup)
	if err != nil {
		return nil, err
	}
	document["cu"], err = json.Marshal(section)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(document, "", "  ")
}

// Materialize checks every output before writing and never follows output symlinks.
// Reuse keeps editable template/model files; force replaces only the known generated files.
func Materialize(ctx context.Context, target string, choices Choices, force bool) (Generation, error) {
	choices, err := choices.Normalize()
	if err != nil {
		return Generation{}, err
	}
	if choices.SubscriptionID == "" || choices.TenantID == "" {
		return Generation{}, invalid("missing_infra_scope", "Generation requires a resolved subscription and tenant.")
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return Generation{}, err
	}
	if err := ctx.Err(); err != nil {
		return Generation{}, err
	}
	entries, err := os.ReadDir(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Generation{}, invalid("invalid_output_directory", "The output directory could not be inspected.")
	}
	hasContent := len(entries) != 0
	isFile := func(path string) bool {
		exists, err := localio.Check(filepath.Join(target, path))
		return err == nil && exists
	}
	existingTemplate := hasContent && isFile("azure.yaml") && isFile("infra/main.bicep")
	reuse := existingTemplate && !force
	if hasContent && !force && !reuse {
		return Generation{}, invalid("nonempty_output_directory",
			"The target is not an existing generated project; choose an empty directory or explicitly use --force.")
	}
	envPath := filepath.Join(".azure", choices.Environment, ".env")
	envConfigPath := filepath.Join(".azure", choices.Environment, "config.json")
	const parameterPath = "infra/main.parameters.json"
	writes := map[string][]byte{}
	replaces := map[string]bool{envPath: true, envConfigPath: true, parameterPath: true, ".azure/config.json": true}
	if !reuse {
		err := fs.WalkDir(assets, "assets", func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			body, err := assets.ReadFile(path)
			if err != nil {
				return err
			}
			relative := strings.TrimPrefix(path, "assets/")
			writes[relative], replaces[relative] = body, force
			return nil
		})
		if err != nil {
			return Generation{}, err
		}
	}
	config := map[string]json.RawMessage{}
	body, err := localio.Read(filepath.Join(target, envPath))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Generation{}, err
	}
	existingEnv := string(body)
	if reuse {
		body, err := localio.Read(filepath.Join(target, ".azure/config.json"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Generation{}, err
		}
		if err == nil && (json.Unmarshal(body, &config) != nil || config == nil) {
			return Generation{}, invalid("invalid_project_config", "Existing azd config must be a JSON object.")
		}
	}
	if force || !isFile("infra/models.json") {
		writes["infra/models.json"] = []byte("[]\n")
		replaces["infra/models.json"] = force
	}
	parameterBody := writes[parameterPath]
	if existingTemplate {
		parameterBody, err = localio.Read(filepath.Join(target, parameterPath))
		if err != nil {
			return Generation{}, err
		}
	}
	renderedParameters, literals, err := renderParameters(parameterBody)
	if err != nil {
		return Generation{}, err
	}
	migrations, err := migrateResourceEnvironments(target, choices.Environment, literals)
	if err != nil {
		return Generation{}, err
	}
	for path, body := range migrations {
		writes[path], replaces[path] = body, true
	}
	if force {
		renderedParameters, _, err = renderParameters(writes[parameterPath])
		if err != nil {
			return Generation{}, err
		}
	}
	writes[parameterPath] = renderedParameters
	writes[parameterPath] = append(writes[parameterPath], '\n')
	envConfigBody, err := localio.Read(filepath.Join(target, envConfigPath))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Generation{}, err
	}
	if errors.Is(err, fs.ErrNotExist) {
		envConfigBody = []byte("{}")
	}
	writes[envConfigPath], err = renderEnvironmentConfig(envConfigBody, choices)
	if err != nil {
		return Generation{}, err
	}
	writes[envConfigPath] = append(writes[envConfigPath], '\n')
	config["version"] = json.RawMessage("1")
	config["defaultEnvironment"], err = json.Marshal(choices.Environment)
	if err != nil {
		return Generation{}, err
	}
	writes[".azure/config.json"], err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Generation{}, err
	}
	writes[".azure/config.json"] = append(writes[".azure/config.json"], '\n')
	writes[envPath] = []byte(MergeEnvironment(existingEnv, choices))
	paths := make([]string, 0, len(writes))
	for relative := range writes {
		paths = append(paths, relative)
		exists, err := localio.Check(filepath.Join(target, relative))
		if err != nil {
			return Generation{}, invalid("unsafe_project_output", "A generated output or its parent is unsafe.")
		}
		if exists && !replaces[relative] {
			return Generation{}, invalid("project_output_exists", "A generated output already exists.")
		}
	}
	slices.Sort(paths)
	result := Generation{Choices: choices, OutputDirectory: target, Files: []string{},
		ReusedExistingTemplate: reuse, NextSteps: []string{"cd " + shellQuote(target), "azd auth login", "azd up"}}
	for _, relative := range paths {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := localio.Write(filepath.Join(target, relative), writes[relative], replaces[relative]); err != nil {
			return result, &cuclient.Error{Kind: cuclient.ErrorService, Code: "project_write_failed",
				Message: "Project generation could not finish; completed files are retained.", Cause: err}
		}
		if strings.HasSuffix(relative, ".sh") {
			if err := localio.MakeExecutable(filepath.Join(target, relative)); err != nil {
				return result, err
			}
		}
		result.Files = append(result.Files, filepath.ToSlash(relative))
	}
	return result, ctx.Err()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
