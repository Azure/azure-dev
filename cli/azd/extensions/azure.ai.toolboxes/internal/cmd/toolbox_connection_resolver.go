// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/connections"
)

// projectConnection is the minimal slice of an Azure project connection that
// toolbox commands need: the ARM `id` (used as `project_connection_id`), the
// category (drives the tool-entry shape), the short name, and the data-plane
// `target` (becomes `server_url` on MCP tool entries).
type projectConnection struct {
	ID       string
	Category connections.ConnectionType
	Name     string
	Target   string
}

// connectionResolver is the seam that tests substitute with stub implementations.
type connectionResolver interface {
	resolveConnection(ctx context.Context, endpoint, name string) (*projectConnection, error)
}

type connectionDescriptorRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type azdConnectionDescriptorRunner struct{}

func (azdConnectionDescriptorRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "azd", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("azd %s failed: %s", strings.Join(args, " "), message)
	}
	return stdout.Bytes(), nil
}

type defaultConnectionResolver struct {
	runner connectionDescriptorRunner
}

func (r defaultConnectionResolver) resolveConnection(
	ctx context.Context, endpoint, name string,
) (*projectConnection, error) {
	runner := r.runner
	if runner == nil {
		runner = azdConnectionDescriptorRunner{}
	}
	output, err := runner.Run(
		ctx,
		"ai", "connection", "show", name,
		"--project-endpoint", endpoint,
		"--output", "json",
		"--no-prompt",
	)
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeConnectionNotFound,
			fmt.Sprintf("failed to resolve connection %q: %s", name, err),
			"run `azd ai connection show <name>` to verify the connection, then retry",
		)
	}
	var descriptor struct {
		ID     string                     `json:"id"`
		Name   string                     `json:"name"`
		Kind   connections.ConnectionType `json:"kind"`
		Target string                     `json:"target"`
	}
	if err := json.Unmarshal(output, &descriptor); err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("connection show returned invalid JSON for %q: %s", name, err),
		)
	}
	if strings.TrimSpace(descriptor.Name) == "" || strings.TrimSpace(descriptor.Kind.String()) == "" {
		return nil, exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("connection show returned an incomplete descriptor for %q", name),
		)
	}
	if descriptor.ID == "" {
		descriptor.ID = descriptor.Name
	}

	return &projectConnection{
		ID:       descriptor.ID,
		Category: descriptor.Kind,
		Name:     descriptor.Name,
		Target:   descriptor.Target,
	}, nil
}

func connectionNotFoundError(name string) error {
	return exterrors.Validation(
		exterrors.CodeConnectionNotFound,
		fmt.Sprintf("connection %q was not found on the project", name),
		"run `azd ai connection list` to see available connections",
	)
}
