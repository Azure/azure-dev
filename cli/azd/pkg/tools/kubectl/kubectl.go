package kubectl

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type KubectlCli interface {
	tools.ExternalTool
	GetNodes(ctx context.Context) ([]Node, error)
	ApplyFile(ctx context.Context, filename string) error
	ApplyDirectory(ctx context.Context, directoryPath string) error
}

type kubectlCli struct {
	tools.ExternalTool
	commandRunner exec.CommandRunner
}

func (cli *kubectlCli) CheckInstalled(ctx context.Context) (bool, error) {
	return true, nil
}

func (cli *kubectlCli) InstallUrl() string {
	return "https://aka.ms/azure-dev/kubectl-install"
}

func (cli *kubectlCli) Name() string {
	return "kubectl"
}

func (cli *kubectlCli) GetNodes(ctx context.Context) ([]Node, error) {
	res, err := cli.executeCommand(ctx,
		"get", "nodes",
		"-o", "json",
	)
	if err != nil {
		return nil, fmt.Errorf("kubectl get nodes: %w", err)
	}

	var listResult ListResult
	if err := json.Unmarshal([]byte(res.Stdout), &listResult); err != nil {
		return nil, fmt.Errorf("unmarshaling json: %w", err)
	}

	nodes := []Node{}
	for _, item := range listResult.Items {
		metadata := item["metadata"].(map[string]any)

		nodes = append(nodes, Node{
			Name: metadata["name"].(string),
		})
	}

	return nodes, nil
}

func (cli *kubectlCli) ApplyFile(ctx context.Context, filename string) error {
	_, err := cli.executeCommand(ctx, "apply", "-f", filename)
	if err != nil {
		return fmt.Errorf("kubectl apply -f: %w", err)
	}

	return nil
}

func (cli *kubectlCli) ApplyDirectory(ctx context.Context, directoryPath string) error {
	_, err := cli.executeCommand(ctx, "apply", "-k", directoryPath)
	if err != nil {
		return fmt.Errorf("kubectl apply -k: %w", err)
	}

	return nil
}

func (cli *kubectlCli) executeCommand(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("kubectl").
		AppendParams(args...).
		WithEnrichError(true)

	return cli.commandRunner.Run(ctx, runArgs)
}

func NewKubectl(ctx context.Context) KubectlCli {
	return &kubectlCli{
		commandRunner: exec.GetCommandRunner(ctx),
	}
}
