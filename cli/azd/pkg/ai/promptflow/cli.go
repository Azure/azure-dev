package promptflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

type Cli struct {
	env    *environment.Environment
	aiTool *ai.Tool
}

func NewCli(env *environment.Environment, aiTool *ai.Tool) *Cli {
	return &Cli{
		env:    env,
		aiTool: aiTool,
	}
}

func (c *Cli) CreateOrUpdate(
	ctx context.Context,
	workspaceName string,
	resourceGroupName string,
	flow *Flow,
	overrides map[string]osutil.ExpandableString,
) (*Flow, error) {
	if overrides == nil {
		overrides = map[string]osutil.ExpandableString{}
	}

	getArgs := []string{
		"get",
		"-s", c.env.GetSubscriptionId(),
		"-w", workspaceName,
		"-g", resourceGroupName,
		"-n", flow.DisplayName,
	}

	var createOrUpdateArgs []string
	_, err := c.aiTool.Run(ctx, ai.PromptFlowClient, getArgs...)
	if err == nil {
		createOrUpdateArgs = []string{"update", "-n", flow.DisplayName}
	} else {
		createOrUpdateArgs = []string{"create", "-n", flow.DisplayName, "-f", flow.Path}
	}

	createOrUpdateArgs = append(createOrUpdateArgs,
		"-s", c.env.GetSubscriptionId(),
		"-w", workspaceName,
		"-g", resourceGroupName,
	)

	if flow.Description != "" {
		overrides["description"] = osutil.NewExpandableString(flow.Description)
	}
	if flow.Type != "" {
		overrides["type"] = osutil.NewExpandableString(string(flow.Type))
	}

	for key, value := range overrides {
		expandedValue, err := value.Envsubst(c.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("failed to expand value for key %s: %w", key, err)
		}

		createOrUpdateArgs = append(createOrUpdateArgs, "--set", fmt.Sprintf("%s=%s", key, expandedValue))
	}

	result, err := c.aiTool.Run(ctx, ai.PromptFlowClient, createOrUpdateArgs...)
	if err != nil {
		return nil, fmt.Errorf("flow operation failed: %w", err)
	}

	var existingFlow *Flow
	err = json.Unmarshal([]byte(result.Stdout), &existingFlow)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal flow: %w", err)
	}

	return existingFlow, nil
}
