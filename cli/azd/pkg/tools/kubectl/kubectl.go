package kubectl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type KubectlCli interface {
	tools.ExternalTool
	Cwd(cwd string)
	SetEnv(env ...string)
	GetNodes(ctx context.Context, flags *KubeCliFlags) ([]Node, error)
	ApplyFiles(ctx context.Context, path string, flags *KubeCliFlags) (*exec.RunResult, error)
	ApplyKustomize(ctx context.Context, path string, flags *KubeCliFlags) (*exec.RunResult, error)
	ConfigView(ctx context.Context, merge bool, flatten bool, flags *KubeCliFlags) (*exec.RunResult, error)
	ConfigUseContext(ctx context.Context, name string, flags *KubeCliFlags) (*exec.RunResult, error)
	CreateNamespace(ctx context.Context, name string, flags *KubeCliFlags) (*exec.RunResult, error)
	CreateSecretGenericFromLiterals(ctx context.Context, name string, secrets []string, flags *KubeCliFlags) (*exec.RunResult, error)
	ApplyPipe(ctx context.Context, result exec.RunResult, flags *KubeCliFlags) (*exec.RunResult, error)
}

type kubectlCli struct {
	tools.ExternalTool
	commandRunner exec.CommandRunner
	env           []string
	cwd           string
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

func (cli *kubectlCli) SetEnv(env ...string) {
	cli.env = env
}

func (cli *kubectlCli) Cwd(cwd string) {
	cli.cwd = cwd
}

func (cli *kubectlCli) ConfigUseContext(ctx context.Context, name string, flags *KubeCliFlags) (*exec.RunResult, error) {
	res, err := cli.executeCommand(ctx, flags, "config", "use-context", name)
	if err != nil {
		return nil, fmt.Errorf("failed setting kubectl context: %w", err)
	}

	return &res, nil
}

func (cli *kubectlCli) ConfigView(ctx context.Context, merge bool, flatten bool, flags *KubeCliFlags) (*exec.RunResult, error) {
	kubeConfigDir, err := getKubeConfigDir()
	if err != nil {
		return nil, err
	}

	args := []string{"config", "view"}
	if merge {
		args = append(args, "--merge")
	}
	if flatten {
		args = append(args, "--flatten")
	}

	runArgs := exec.NewRunArgs("kubectl", args...).
		WithCwd(kubeConfigDir).
		WithEnv(cli.env)

	res, err := cli.executeCommandWithArgs(ctx, runArgs, flags)
	if err != nil {
		return nil, fmt.Errorf("kubectl config view: %w", err)
	}

	return &res, nil
}

func (cli *kubectlCli) GetNodes(ctx context.Context, flags *KubeCliFlags) ([]Node, error) {
	res, err := cli.executeCommand(ctx, flags,
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

func (cli *kubectlCli) ApplyPipe(ctx context.Context, result exec.RunResult, flags *KubeCliFlags) (*exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("kubectl", "apply", "-f", "-").
		WithStdIn(strings.NewReader(result.Stdout))

	res, err := cli.executeCommandWithArgs(ctx, runArgs, flags)
	if err != nil {
		return nil, fmt.Errorf("kubectl apply -f: %w", err)
	}

	return &res, nil
}

func (cli *kubectlCli) ApplyFiles(ctx context.Context, path string, flags *KubeCliFlags) (*exec.RunResult, error) {

	res, err := cli.executeCommand(ctx, flags, "apply", "-f", path)
	if err != nil {
		return nil, fmt.Errorf("kubectl apply -f: %w", err)
	}

	return &res, nil
}

func (cli *kubectlCli) ApplyKustomize(ctx context.Context, path string, flags *KubeCliFlags) (*exec.RunResult, error) {
	res, err := cli.executeCommand(ctx, flags, "apply", "-k", path)
	if err != nil {
		return nil, fmt.Errorf("kubectl apply -k: %w", err)
	}

	return &res, nil
}

func (cli *kubectlCli) CreateSecretGenericFromLiterals(ctx context.Context, name string, secrets []string, flags *KubeCliFlags) (*exec.RunResult, error) {
	args := []string{"create", "secret", "generic", name}
	for _, secret := range secrets {
		args = append(args, fmt.Sprintf("--from-literal=%s", secret))
	}

	res, err := cli.executeCommand(ctx, flags, args...)
	if err != nil {
		return nil, fmt.Errorf("kubectl create secret generic --from-env-file: %w", err)
	}

	return &res, nil
}

type KubeCliFlags struct {
	Namespace string
	DryRun    string
	Output    string
}

func (cli *kubectlCli) CreateNamespace(ctx context.Context, name string, flags *KubeCliFlags) (*exec.RunResult, error) {
	args := []string{"create", "namespace", name}

	res, err := cli.executeCommand(ctx, flags, args...)
	if err != nil {
		return nil, fmt.Errorf("kubectl create namespace: %w", err)
	}

	return &res, nil
}

func (cli *kubectlCli) executeCommand(ctx context.Context, flags *KubeCliFlags, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("kubectl").
		AppendParams(args...)

	return cli.executeCommandWithArgs(ctx, runArgs, flags)
}

func (cli *kubectlCli) executeCommandWithArgs(ctx context.Context, args exec.RunArgs, flags *KubeCliFlags) (exec.RunResult, error) {
	args = args.WithEnrichError(true)
	if cli.cwd != "" {
		args = args.WithCwd(cli.cwd)
	}

	if flags != nil {
		if flags.DryRun != "" {
			args = args.AppendParams(fmt.Sprintf("--dry-run=%s", flags.DryRun))
		}
		if flags.Namespace != "" {
			args = args.AppendParams("-n", flags.Namespace)
		}
		if flags.Output != "" {
			args = args.AppendParams("-o", flags.Output)
		}
	}

	return cli.commandRunner.Run(ctx, args)
}

func NewKubectl(commandRunner exec.CommandRunner) KubectlCli {
	return &kubectlCli{
		commandRunner: commandRunner,
	}
}
