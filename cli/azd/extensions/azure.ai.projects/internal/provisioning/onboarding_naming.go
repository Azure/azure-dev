// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore uniquestring

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var officialResourceToken = regexp.MustCompile(`^[a-z2-7]{13}$`)

func evaluateBYOPlanFunction(
	ctx context.Context, compiler bicepCompiler, name string, args []string,
) (value string, err error) {
	if name != "uniquestring" && name != "guid" {
		return "", fmt.Errorf("unsupported local Bicep plan function")
	}
	if name == "uniquestring" {
		name = "uniqueString"
	}
	dir, err := os.MkdirTemp("", "azd-foundry-plan-")
	if err != nil {
		return "", fmt.Errorf("create Bicep plan evaluation directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode Bicep plan arguments: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "arguments.json"), raw, 0o600); err != nil {
		return "", fmt.Errorf("write Bicep plan arguments: %w", err)
	}
	arguments := make([]string, len(args))
	for i := range args {
		arguments[i] = fmt.Sprintf("arguments[%d]", i)
	}
	probe := "using none\nvar arguments = loadJsonContent('./arguments.json')\nparam result = " +
		name + "(" + strings.Join(arguments, ", ") + ")\n"
	path := filepath.Join(dir, "plan.bicepparam")
	if err := os.WriteFile(path, []byte(probe), 0o600); err != nil {
		return "", fmt.Errorf("write local Bicep plan expression: %w", err)
	}
	result, err := compiler.BuildBicepParam(ctx, path, []string{"BICEP_PARAMETERS_OVERRIDES={}"})
	if err != nil {
		return "", fmt.Errorf("evaluate template naming with azd-managed Bicep: %w", err)
	}
	var envelope struct {
		ParametersJSON string `json:"parametersJson"`
	}
	if err := json.Unmarshal([]byte(result.Compiled), &envelope); err != nil {
		return "", fmt.Errorf("decode Bicep plan evaluation: %w", err)
	}
	var parameters struct {
		Parameters map[string]struct {
			Value string `json:"value"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(envelope.ParametersJSON), &parameters); err != nil {
		return "", fmt.Errorf("decode Bicep plan parameters: %w", err)
	}
	value = parameters.Parameters["result"].Value
	if value == "" || name == "uniqueString" && !officialResourceToken.MatchString(value) {
		return "", fmt.Errorf("Bicep returned an invalid local plan value")
	}
	return value, nil
}
