// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package az

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// NewCli creates a new AzCli instance with the provided command runner.
// The command runner is used to execute Azure CLI commands.
//
// Parameters:
//   - commandRunner: An implementation of exec.CommandRunner interface to execute commands
//
// Returns:
//   - AzCli: A new AzCli instance
//   - error: An error if initialization fails, nil otherwise
func NewCli(
	commandRunner exec.CommandRunner,
) (AzCli, error) {
	return AzCli{
		runner: commandRunner,
	}, nil
}

// AzCli represents a wrapper around the Azure CLI command-line interface.
// It provides functionality to execute Azure CLI commands through a command runner.
type AzCli struct {
	runner exec.CommandRunner
}

// CheckInstalled checks if the Azure CLI ('az') is available in the system's PATH.
// It verifies whether the 'az' command can be found and executed from any directory.
// Returns nil if the Azure CLI is installed and accessible, or an error if not found.
func (az AzCli) CheckInstalled() error {
	return az.runner.ToolInPath("az")
}

// AzAccountUser represents a user account in Azure with basic identification details.
// It contains the user name and account type information as retrieved from Azure CLI.
type AzAccountUser struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// AzAccountResponse represents the structure of an Azure account response
// containing user information from the Azure CLI account output.
type AzAccountResponse struct {
	User AzAccountUser `json:"user"`
}

// Account retrieves the current Azure account information.
// It executes 'az account show' command and returns the account details in a structured format.
// If az CLI is not authenticated, it returns an error indicating authentication is required.
// Returns AzAccountResponse containing account details or an error if the operation fails.
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
