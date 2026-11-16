// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/drone/envsubst"
)

// NewBicepCompiler retains normal tool setup and module restoration.
func NewBicepCompiler() *bicep.Cli {
	console := input.NewConsole(
		false,
		true,
		input.Writers{Output: os.Stdout},
		input.ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		},
		nil,
		nil,
	)
	return bicep.NewCli(console, exec.NewCommandRunner(nil))
}

// BicepParamCompiler resolves expressions and environment reads.
type BicepParamCompiler interface {
	BuildBicepParam(context.Context, string, []string) (bicep.BuildResult, error)
}

// ARMParameterValue resolves ARM names case-insensitively.
func ARMParameterValue(template, parameters map[string]any, name string) any {
	for key, value := range parameters {
		if strings.EqualFold(key, name) {
			if entry, ok := value.(map[string]any); ok {
				return entry["value"]
			}
		}
	}
	defaults, _ := template["parameters"].(map[string]any)
	for key, value := range defaults {
		if strings.EqualFold(key, name) {
			if entry, ok := value.(map[string]any); ok {
				return entry["defaultValue"]
			}
		}
	}
	return nil
}

// CompileBicepParameters reads the template and parameter envelope.
func CompileBicepParameters(
	ctx context.Context,
	compiler BicepParamCompiler,
	sourcePath string,
	envValues map[string]string,
) (map[string]any, map[string]any, error) {
	env := make([]string, 0, len(envValues))
	for _, key := range slices.Sorted(maps.Keys(envValues)) {
		env = append(env, key+"="+envValues[key])
	}
	result, err := compiler.BuildBicepParam(ctx, sourcePath, env)
	if err != nil {
		if _, structured := errors.AsType[*azdext.LocalError](err); structured {
			return nil, nil, err
		}
		return nil, nil, &azdext.LocalError{
			Code: "ondisk_bicep_compile_failed", Category: azdext.LocalErrorCategoryValidation,
			Message:    fmt.Sprintf("compile %s: %s", sourcePath, err),
			Suggestion: "fix the bicep errors above and re-run `azd provision`",
		}
	}
	var envelope struct {
		TemplateJSON   string `json:"templateJson"`
		ParametersJSON string `json:"parametersJson"`
	}
	if err := json.Unmarshal([]byte(result.Compiled), &envelope); err != nil {
		return nil, nil, &azdext.LocalError{
			Code: "ondisk_bicep_parse_failed", Category: azdext.LocalErrorCategoryInternal,
			Message: fmt.Sprintf("parse bicepparam envelope from %s: %s", sourcePath, err),
		}
	}
	if envelope.TemplateJSON == "" {
		return nil, nil, &azdext.LocalError{
			Code: "ondisk_bicep_parse_failed", Category: azdext.LocalErrorCategoryInternal,
			Message: fmt.Sprintf("compiled ARM JSON from %s is empty", sourcePath),
		}
	}
	var template map[string]any
	if err := json.Unmarshal([]byte(envelope.TemplateJSON), &template); err != nil {
		return nil, nil, &azdext.LocalError{
			Code: "ondisk_bicep_parse_failed", Category: azdext.LocalErrorCategoryInternal,
			Message: fmt.Sprintf("parse compiled ARM JSON from %s: %s", sourcePath, err),
		}
	}
	parameters, err := parseARMParameters([]byte(envelope.ParametersJSON), sourcePath)
	return template, parameters, err
}

// LoadARMParametersFile substitutes each parameter independently.
// Explicit envValues entries take precedence over process environment values.
// Missing variables drop only strings that resolve to an empty value.
func LoadARMParametersFile(sourcePath string, envValues map[string]string) (map[string]any, error) {
	//nolint:gosec // validated project infrastructure parameter path
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, &azdext.LocalError{
			Code: "ondisk_parameters_invalid", Category: azdext.LocalErrorCategoryInternal,
			Message: fmt.Sprintf("read %s: %s", sourcePath, err),
		}
	}
	parameters, err := parseARMParameters(raw, sourcePath)
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]any, len(parameters))
	for name, entry := range parameters {
		value, err := substituteParameter(entry, sourcePath, name, envValues)
		if err != nil {
			return nil, err
		}
		if value != nil {
			resolved[name] = value
		}
	}
	return resolved, nil
}

func substituteParameter(
	raw any, sourcePath, name string, envValues map[string]string,
) (any, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, &azdext.LocalError{
			Code: "ondisk_parameters_invalid", Category: azdext.LocalErrorCategoryInternal,
			Message: fmt.Sprintf("re-encode parameter %q in %s: %s", name, sourcePath, err),
		}
	}
	hasUnsetEnvVar := false
	substituted, err := envsubst.Eval(string(encoded), func(key string) string {
		value, found := envValues[key]
		if !found {
			value, found = os.LookupEnv(key)
		}
		if !found {
			hasUnsetEnvVar = true
			return ""
		}
		// Escape values inside the JSON-encoded parameter entry.
		escaped, _ := json.Marshal(value)
		return string(escaped[1 : len(escaped)-1])
	})
	if err != nil {
		return nil, &azdext.LocalError{
			Code: "ondisk_parameters_invalid", Category: azdext.LocalErrorCategoryValidation,
			Message:    fmt.Sprintf("substitute env vars in parameter %q of %s: %s", name, sourcePath, err),
			Suggestion: "check for malformed ${VAR} references in the parameters file",
		}
	}
	var resolved any
	if err := json.Unmarshal([]byte(substituted), &resolved); err != nil {
		return nil, &azdext.LocalError{
			Code: "ondisk_parameters_invalid", Category: azdext.LocalErrorCategoryValidation,
			Message:    fmt.Sprintf("parse parameter %q in %s after substitution: %s", name, sourcePath, err),
			Suggestion: "ensure the substituted value is valid JSON",
		}
	}
	if entry, ok := resolved.(map[string]any); ok && hasUnsetEnvVar {
		if value, ok := entry["value"].(string); ok && value == "" {
			return nil, nil
		}
	}
	return resolved, nil
}

func parseARMParameters(raw []byte, sourcePath string) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var document struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, &azdext.LocalError{
			Code: "ondisk_parameters_invalid", Category: azdext.LocalErrorCategoryValidation,
			Message:    fmt.Sprintf("parse %s as ARM parameters JSON: %s", sourcePath, err),
			Suggestion: "verify the file matches the ARM deploymentParameters schema",
		}
	}
	if document.Parameters == nil {
		return map[string]any{}, nil
	}
	return document.Parameters, nil
}
