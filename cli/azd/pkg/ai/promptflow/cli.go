package promptflow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

type Cli struct {
	env           *environment.Environment
	commandRunner exec.CommandRunner
}

func NewCli(env *environment.Environment, commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		env:           env,
		commandRunner: commandRunner,
	}
}

func (c *Cli) List(ctx context.Context, workspaceName string, resourceGroupName string) ([]*Flow, error) {
	listArgs := exec.NewRunArgs(
		"pfazure", "flow", "list",
		"--workspace", workspaceName,
		"--resource-group", resourceGroupName,
	)

	result, err := c.commandRunner.Run(ctx, listArgs)
	if err != nil {
		return nil, fmt.Errorf("failed listing flows: %w", err)
	}

	var flows []*Flow
	if err := json.Unmarshal([]byte(result.Stdout), &flows); err != nil {
		return nil, fmt.Errorf("failed unmarshalling flows: %w", err)
	}

	return flows, nil
}

func (c *Cli) Get(ctx context.Context, workspaceName string, resourceGroupName string, flowName string) (*Flow, error) {
	existingFlows, err := c.List(ctx, workspaceName, resourceGroupName)
	if err != nil {
		return nil, err
	}

	index := slices.IndexFunc(existingFlows, func(f *Flow) bool {
		return f.DisplayName == flowName
	})

	if index == -1 {
		return nil, fmt.Errorf("flow %s not found", flowName)
	}

	return existingFlows[index], nil
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

	args := exec.NewRunArgs("pfazure", "flow")

	existingFlow, err := c.Get(ctx, workspaceName, resourceGroupName, flow.DisplayName)
	if existingFlow == nil || err != nil {
		args = args.AppendParams("create", "--flow", flow.Path)
	} else {
		args = args.AppendParams("update", "--flow", existingFlow.Name)
	}

	args = args.AppendParams(
		"--workspace", workspaceName,
		"--resource-group", resourceGroupName,
	)

	overrides["display_name"] = osutil.NewExpandableString(flow.DisplayName)
	overrides["description"] = osutil.NewExpandableString(flow.Description)
	overrides["type"] = osutil.NewExpandableString(string(flow.Type))

	for key, value := range overrides {
		expandedValue, err := value.Envsubst(c.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("failed to expand value for key %s: %w", key, err)
		}

		args = args.AppendParams("--set", fmt.Sprintf("%s=%s", key, expandedValue))
	}

	_, err = c.commandRunner.Run(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("flow operation failed: %w", err)
	}

	existingFlow, err = c.Get(ctx, workspaceName, resourceGroupName, flow.DisplayName)
	if err != nil {
		return nil, err
	}

	return existingFlow, nil
}
