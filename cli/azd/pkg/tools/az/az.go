// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package az

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

func NewCli(
	commandRunner exec.CommandRunner,
) (AzCli, error) {
	return AzCli{
		runner: commandRunner,
	}, nil
}

type AzCli struct {
	runner exec.CommandRunner
}

func (az AzCli) CheckInstalled() error {
	return tools.ToolInPath("az")
}

type AzAccountUser struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type AzAccountResponse struct {
	User AzAccountUser `json:"user"`
}

func (az AzCli) Account(ctx context.Context) (AzAccountResponse, error) {
	cmd := exec.NewRunArgs("az", "account", "show", "--output", "json")

	output, err := az.runner.Run(ctx, cmd)
	if err != nil {
		return AzAccountResponse{}, err
	}

	if strings.Contains(output.Stderr, "az login") || strings.Contains(output.Stdout, "az login") {
		return AzAccountResponse{}, fmt.Errorf("az is not authenticated.")
	}

	var accountResponse AzAccountResponse
	if err := json.Unmarshal([]byte(output.Stdout), &accountResponse); err != nil {
		return AzAccountResponse{}, err
	}

	return accountResponse, nil
}
