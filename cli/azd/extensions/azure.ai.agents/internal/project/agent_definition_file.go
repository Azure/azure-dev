// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"
	agentSchemas "azureaiagent/schemas"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

const agentDefinitionSchemaURI = "mem://azure.ai.agent.json"

// coreServiceKeys are parsed by azd before extension handlers.
var coreServiceKeys = []string{
	"apiVersion", "condition", "config", "dist", "docker", "env", "hooks",
	"host", "image", "infra", "k8s", "language", "module", "project",
	"remoteBuild", "resourceGroup", "resourceName", "uses",
}

var compiledAgentDefinitionSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	var schema map[string]any
	if err := json.Unmarshal(agentSchemas.AgentDefinitionSchema(), &schema); err != nil {
		return nil, fmt.Errorf("decode embedded agent schema: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(agentDefinitionSchemaURI, schema); err != nil {
		return nil, fmt.Errorf("load embedded agent schema: %w", err)
	}
	compiled, err := compiler.Compile(agentDefinitionSchemaURI)
	if err != nil {
		return nil, fmt.Errorf("compile embedded agent schema: %w", err)
	}
	return compiled, nil
})

// AgentDefinitionFile holds a validated direct definition.
type AgentDefinitionFile struct {
	// Path is the normalized project-relative path to the input file.
	Path string
	// Properties contains the input document before reference expansion.
	Properties map[string]any
	// Kind and Name are the identity accepted by runtime validation.
	Kind agent_yaml.AgentKind
	Name string
}

// LoadAgentDefinitionFile loads and validates a direct definition.
// Relative paths use projectRoot. serviceName preserves the
// existing prompt-agent name fallback used by runtime validation.
func LoadAgentDefinitionFile(
	projectRoot string,
	filePath string,
	serviceName string,
) (AgentDefinitionFile, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return AgentDefinitionFile{}, invalidAgentFilePath(
			filePath,
			"project root is empty",
		)
	}
	projectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return AgentDefinitionFile{}, invalidAgentFilePath(
			filePath,
			fmt.Sprintf("cannot resolve project root: %s", err),
		)
	}

	resolvedPath, relativePath, err := resolveAgentDefinitionFilePath(projectRoot, filePath)
	if err != nil {
		return AgentDefinitionFile{}, err
	}

	data, err := os.ReadFile(resolvedPath)
	if errors.Is(err, os.ErrNotExist) {
		return AgentDefinitionFile{}, exterrors.Dependency(
			exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("agent definition file %q was not found", relativePath),
			"check that the file path is correct and the file exists",
		)
	}
	if err != nil {
		return AgentDefinitionFile{}, invalidAgentFilePath(
			filePath,
			fmt.Sprintf("cannot read file: %s", err),
		)
	}

	properties, err := decodeAgentDefinitionDocument(data)
	if err != nil {
		return AgentDefinitionFile{}, invalidAgentDefinitionFile(relativePath, err)
	}
	if err := validateAgentDefinitionDocumentShape(properties); err != nil {
		return AgentDefinitionFile{}, invalidAgentDefinitionFile(relativePath, err)
	}

	serviceProperties, err := structpb.NewStruct(properties)
	if err != nil {
		return AgentDefinitionFile{}, invalidAgentDefinitionFile(relativePath, err)
	}
	resolved, err := resolveServiceProps(serviceProperties, serviceName, projectRoot)
	if err != nil {
		return AgentDefinitionFile{}, invalidAgentDefinitionFile(relativePath, err)
	}
	effectiveProperties := resolved.AsMap()
	if err := validateAgentDefinitionDocumentShape(effectiveProperties); err != nil {
		return AgentDefinitionFile{}, invalidAgentDefinitionFile(relativePath, err)
	}
	if err := validateAgentDefinitionSchema(relativePath, effectiveProperties); err != nil {
		return AgentDefinitionFile{}, err
	}

	sourceDir := filepath.Dir(relativePath)
	if sourceDir == "." {
		sourceDir = ""
	}
	service := &azdext.ServiceConfig{
		Name:                 serviceName,
		Host:                 "azure.ai.agent",
		RelativePath:         sourceDir,
		AdditionalProperties: resolved,
	}
	validation, err := ValidateAgentServiceDefinition(service, projectRoot)
	if err != nil {
		return AgentDefinitionFile{}, exterrors.ValidationFromError(
			err,
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent definition file %q is not valid", relativePath),
			"fix the direct agent definition and its local references",
		)
	}

	return AgentDefinitionFile{
		Path:       relativePath,
		Properties: properties,
		Kind:       validation.Kind,
		Name:       validation.Name,
	}, nil
}

func resolveAgentDefinitionFilePath(
	projectRoot string,
	filePath string,
) (string, string, error) {
	if strings.TrimSpace(filePath) == "" {
		return "", "", invalidAgentFilePath(filePath, "path is empty")
	}

	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", invalidAgentFilePath(
			filePath,
			fmt.Sprintf("cannot resolve project root: %s", err),
		)
	}

	relativePath := filePath
	if filepath.IsAbs(filePath) {
		absolutePath, err := filepath.Abs(filePath)
		if err != nil {
			return "", "", invalidAgentFilePath(
				filePath,
				fmt.Sprintf("cannot resolve absolute path: %s", err),
			)
		}
		relativePath, err = filepath.Rel(rootAbs, absolutePath)
		if err != nil {
			return "", "", invalidAgentFilePath(
				filePath,
				fmt.Sprintf("cannot make path relative to the project: %s", err),
			)
		}
	}

	resolvedPath, err := paths.Join(rootAbs, filepath.ToSlash(relativePath))
	if err != nil {
		return "", "", invalidAgentFilePath(filePath, err.Error())
	}
	extension := strings.ToLower(filepath.Ext(resolvedPath))
	if extension != ".yaml" && extension != ".yml" && extension != ".json" {
		return "", "", invalidAgentFilePath(
			filePath,
			"expected a YAML or JSON file with a .yaml, .yml, or .json extension",
		)
	}

	info, err := os.Stat(resolvedPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", exterrors.Dependency(
			exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("agent definition file %q was not found", filePath),
			"check that the file path is correct and the file exists",
		)
	}
	if err != nil {
		return "", "", invalidAgentFilePath(filePath, err.Error())
	}
	if !info.Mode().IsRegular() {
		return "", "", invalidAgentFilePath(filePath, "path must identify a regular file")
	}

	relativePath, err = filepath.Rel(rootAbs, resolvedPath)
	if err != nil {
		return "", "", invalidAgentFilePath(
			filePath,
			fmt.Sprintf("cannot make path relative to the project: %s", err),
		)
	}
	return resolvedPath, filepath.ToSlash(relativePath), nil
}

func decodeAgentDefinitionDocument(data []byte) (map[string]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var document any
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("file is empty")
		}
		return nil, fmt.Errorf("parse YAML or JSON: %w", err)
	}

	properties, ok := document.(map[string]any)
	if !ok || len(properties) == 0 {
		return nil, fmt.Errorf("document must be a non-empty mapping object")
	}

	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("file must contain exactly one document")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse trailing content: %w", err)
	}

	return properties, nil
}

func validateAgentDefinitionDocumentShape(properties map[string]any) error {
	for _, key := range []string{"services", "pipeline", "requiredVersions"} {
		if _, found := properties[key]; found {
			return fmt.Errorf(
				"project configuration field %q is not a direct agent definition",
				key,
			)
		}
	}

	if _, found := properties["template"]; found {
		return fmt.Errorf("AgentManifest template wrappers are not supported")
	}
	if kind, ok := properties["kind"].(string); ok &&
		strings.EqualFold(strings.TrimSpace(kind), "AgentManifest") {
		return fmt.Errorf("AgentManifest wrappers are not direct agent definitions")
	}

	if _, found := properties["config"]; found {
		return fmt.Errorf("legacy nested config is not a direct agent definition")
	}
	for _, key := range coreServiceKeys {
		if _, found := properties[key]; found {
			return fmt.Errorf("core azure.yaml service field %q is not supported", key)
		}
	}
	return nil
}

func validateAgentDefinitionSchema(
	relativePath string,
	properties map[string]any,
) error {
	schema, err := compiledAgentDefinitionSchema()
	if err != nil {
		return fmt.Errorf("load agent definition schema: %w", err)
	}
	if err := schema.Validate(properties); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf(
				"agent definition file %q does not satisfy schemas/azure.ai.agent.json: %s",
				relativePath,
				err,
			),
			"fix the definition to match schemas/azure.ai.agent.json",
		)
	}
	return nil
}

func invalidAgentDefinitionFile(relativePath string, err error) error {
	return exterrors.ValidationFromError(
		err,
		exterrors.CodeInvalidAgentManifest,
		fmt.Sprintf("agent definition file %q is not valid", relativePath),
		"fix the direct agent definition and its local references",
	)
}

func invalidAgentFilePath(filePath string, reason string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidFilePath,
		fmt.Sprintf("invalid agent definition file path %q: %s", filePath, reason),
		"use an existing .yaml, .yml, or .json file inside the project directory",
	)
}
