package kubectl

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// Executes commands against the Kubernetes CLI
type KubectlCli interface {
	tools.ExternalTool
	// Sets the current working directory
	Cwd(cwd string)
	// Sets the env vars available to the CLI
	SetEnv(env map[string]string)
	// Sets the KUBECONFIG environment variable
	SetKubeConfig(kubeConfig string)
	// Applies one or more files from the specified path
	Apply(ctx context.Context, path string, flags *KubeCliFlags) error
	// Applies manifests from the specified input
	ApplyWithStdIn(ctx context.Context, input string, flags *KubeCliFlags) (*exec.RunResult, error)
	// Applies manifests from the specified file path
	ApplyWithFile(ctx context.Context, filePath string, flags *KubeCliFlags) (*exec.RunResult, error)
	// Views the current k8s configuration including available clusters, contexts & users
	ConfigView(ctx context.Context, merge bool, flatten bool, flags *KubeCliFlags) (*exec.RunResult, error)
	// Sets the k8s context to use for future CLI commands
	ConfigUseContext(ctx context.Context, name string, flags *KubeCliFlags) (*exec.RunResult, error)
	// Creates a new k8s namespace with the specified name
	CreateNamespace(ctx context.Context, name string, flags *KubeCliFlags) (*exec.RunResult, error)
	// Executes a k8s CLI command from the specified arguments and flags
	Exec(ctx context.Context, flags *KubeCliFlags, args ...string) (exec.RunResult, error)
	// Gets the deployment rollout status
	RolloutStatus(ctx context.Context, deploymentName string, flags *KubeCliFlags) (*exec.RunResult, error)
	// Applies the manifests at the specified path using kustomize
	ApplyWithKustomize(ctx context.Context, path string, flags *KubeCliFlags) error
}

type OutputType string

const (
	OutputTypeJson OutputType = "json"
	OutputTypeYaml OutputType = "yaml"
)

type DryRunType string

const (
	DryRunTypeNone DryRunType = "none"
	// If client strategy, only print the object that would be sent
	DryRunTypeClient DryRunType = "client"
	// If server strategy, submit server-side request without persisting the resource.
	DryRunTypeServer DryRunType = "server"
)

// K8s CLI Fags
type KubeCliFlags struct {
	// The namespace to filter the command or create resources
	Namespace string
	// The dry-run type, defaults to empty
	DryRun DryRunType
	// The expected output, typically JSON or YAML
	Output OutputType
}

// templateRoot is the structure of the template available within the test templates that can be used within k8s manifests.
// We have the option to include additional nodes within the template in the future for things like config, etc
type templateRoot struct {
	// The Azd environment variables
	Env map[string]string
}

type kubectlCli struct {
	tools.ExternalTool
	commandRunner exec.CommandRunner
	env           map[string]string
	cwd           string
}

// Creates a new K8s CLI instance
func NewKubectl(commandRunner exec.CommandRunner) KubectlCli {
	return &kubectlCli{
		commandRunner: commandRunner,
		env:           map[string]string{},
	}
}

// Checks whether or not the K8s CLI is installed and available within the PATH
func (cli *kubectlCli) CheckInstalled(ctx context.Context) error {
	if err := tools.ToolInPath("kubectl"); err != nil {
		return err
	}

	// We don't have a minimum required version of kubectl today, but
	// for diagnostics purposes, let's fetch and log the version of kubectl
	// we're using.
	if ver, err := cli.getClientVersion(ctx); err != nil {
		log.Printf("error fetching kubectl version: %s", err)
	} else {
		log.Printf("kubectl version: %s", ver)
	}

	return nil
}

func (cli *kubectlCli) getClientVersion(ctx context.Context) (string, error) {
	versionRes, err := cli.Exec(ctx, &KubeCliFlags{Output: "json"}, "version", "--client=true")
	if err != nil {
		return "", fmt.Errorf("fetching kubectl version: %w", err)
	}

	var versionObj struct {
		ClientVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"clientVersion"`
	}

	if err := json.Unmarshal([]byte(versionRes.Stdout), &versionObj); err != nil {
		return "", fmt.Errorf("parsing kubectl version output: %w", err)
	}

	return versionObj.ClientVersion.GitVersion, nil
}

// Returns the installation URL to install the K8s CLI
func (cli *kubectlCli) InstallUrl() string {
	return "https://aka.ms/azure-dev/kubectl-install"
}

// Gets the name of the Tool
func (cli *kubectlCli) Name() string {
	return "kubectl"
}

// Sets the env vars available to the CLI
func (cli *kubectlCli) SetEnv(envValues map[string]string) {
	for key, value := range envValues {
		cli.env[key] = value
	}
}

// Sets the KUBECONFIG environment variable
func (cli *kubectlCli) SetKubeConfig(kubeConfig string) {
	cli.env[KubeConfigEnvVarName] = kubeConfig
}

// Sets the current working directory
func (cli *kubectlCli) Cwd(cwd string) {
	cli.cwd = cwd
}

// Sets the k8s context to use for future CLI commands
func (cli *kubectlCli) ConfigUseContext(ctx context.Context, name string, flags *KubeCliFlags) (*exec.RunResult, error) {
	res, err := cli.Exec(ctx, flags, "config", "use-context", name)
	if err != nil {
		return nil, fmt.Errorf("failed setting kubectl context: %w", err)
	}

	return &res, nil
}

// Views the current k8s configuration including available clusters, contexts & users
func (cli *kubectlCli) ConfigView(
	ctx context.Context,
	merge bool,
	flatten bool,
	flags *KubeCliFlags,
) (*exec.RunResult, error) {
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
		WithCwd(kubeConfigDir)

	res, err := cli.executeCommandWithArgs(ctx, runArgs, flags)
	if err != nil {
		return nil, fmt.Errorf("kubectl config view: %w", err)
	}

	return &res, nil
}

func (cli *kubectlCli) ApplyWithStdIn(ctx context.Context, input string, flags *KubeCliFlags) (*exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("kubectl", "apply", "-f", "-").
		WithStdIn(strings.NewReader(input))

	res, err := cli.executeCommandWithArgs(ctx, runArgs, flags)
	if err != nil {
		return nil, fmt.Errorf("kubectl apply -f: %w", err)
	}

	return &res, nil
}

func (cli *kubectlCli) ApplyWithFile(ctx context.Context, filePath string, flags *KubeCliFlags) (*exec.RunResult, error) {
	runArgs := exec.NewRunArgs("kubectl", "apply", "-f", filePath)

	res, err := cli.executeCommandWithArgs(ctx, runArgs, flags)
	if err != nil {
		return nil, fmt.Errorf("kubectl apply -f: %w", err)
	}

	return &res, nil
}

// Applies manifests from the specified input
func (cli *kubectlCli) Apply(ctx context.Context, path string, flags *KubeCliFlags) error {
	if err := cli.applyTemplates(ctx, path, flags); err != nil {
		return fmt.Errorf("failed process templates, %w", err)
	}

	return nil
}

// Applies the manifests at the specified path using kustomize
func (cli *kubectlCli) ApplyWithKustomize(ctx context.Context, path string, flags *KubeCliFlags) error {
	runArgs := exec.NewRunArgs("kubectl", "apply", "-k", path)

	_, err := cli.executeCommandWithArgs(ctx, runArgs, flags)
	if err != nil {
		return fmt.Errorf("failing running kubectl apply -k: %w", err)
	}

	return nil
}

// Creates a new k8s namespace with the specified name
func (cli *kubectlCli) CreateNamespace(ctx context.Context, name string, flags *KubeCliFlags) (*exec.RunResult, error) {
	args := []string{"create", "namespace", name}

	res, err := cli.Exec(ctx, flags, args...)
	if err != nil {
		return nil, fmt.Errorf("kubectl create namespace: %w", err)
	}

	return &res, nil
}

// Gets the deployment rollout status
func (cli *kubectlCli) RolloutStatus(
	ctx context.Context,
	deploymentName string,
	flags *KubeCliFlags,
) (*exec.RunResult, error) {
	res, err := cli.Exec(ctx, flags, "rollout", "status", fmt.Sprintf("deployment/%s", deploymentName))
	if err != nil {
		return nil, fmt.Errorf("deployment rollout failed, %w", err)
	}

	return &res, nil
}

// Executes a k8s CLI command from the specified arguments and flags
func (cli *kubectlCli) Exec(ctx context.Context, flags *KubeCliFlags, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("kubectl").
		AppendParams(args...)

	return cli.executeCommandWithArgs(ctx, runArgs, flags)
}

func (cli *kubectlCli) applyTemplate(ctx context.Context, filePath string, flags *KubeCliFlags) (*exec.RunResult, error) {
	k8sTemplate, err := template.ParseFiles(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed parsing template file '%s', %w", filePath, err)
	}

	builder := strings.Builder{}
	err = k8sTemplate.Execute(&builder, templateRoot{Env: cli.env})
	if err != nil {
		return nil, fmt.Errorf("failed executing template file '%s', %w", filePath, err)
	}

	result, err := cli.ApplyWithStdIn(ctx, builder.String(), flags)
	if err != nil {
		return nil, fmt.Errorf("failed applying file '%s', %w", filePath, err)
	}

	return result, nil
}

// Recursively loops through the specified directory and applies all k8s manifests
// If the file is a *.tmpl file, it will be parsed as a template to support environment injection.
// Otherwise the actual file contents will be applied.
func (cli *kubectlCli) applyTemplates(ctx context.Context, directoryPath string, flags *KubeCliFlags) error {
	entries, err := os.ReadDir(directoryPath)
	if err != nil {
		return fmt.Errorf("failed reading files in path, '%s', %w", directoryPath, err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(directoryPath, entry.Name())

		if entry.IsDir() {
			if err := cli.applyTemplates(ctx, entryPath, flags); err != nil {
				return fmt.Errorf("failed applying templates at '%s', %w", entryPath, err)
			}

			continue
		}

		ext := filepath.Ext(entry.Name())
		var err error

		switch ext {
		case ".yaml", ".yml": // Only include yaml files
			fileNameWithoutExtension := strings.TrimSuffix(entry.Name(), ext)
			isTemplateFile := strings.HasSuffix(fileNameWithoutExtension, ".tmpl")

			if isTemplateFile {
				_, err = cli.applyTemplate(ctx, entryPath, flags)
			} else {
				_, err = cli.ApplyWithFile(ctx, entryPath, flags)
			}
		default: // Ignore all other files
			continue
		}

		if err != nil {
			return fmt.Errorf("failed applying file '%s', %w", entryPath, err)
		}
	}

	return nil
}

func (cli *kubectlCli) executeCommandWithArgs(
	ctx context.Context,
	args exec.RunArgs,
	flags *KubeCliFlags,
) (exec.RunResult, error) {
	if cli.cwd != "" {
		args = args.WithCwd(cli.cwd)
	}

	args = args.WithEnv(environ(cli.env))

	if flags != nil {
		if flags.DryRun != "" {
			args = args.AppendParams(fmt.Sprintf("--dry-run=%s", flags.DryRun))
		}
		if flags.Namespace != "" {
			args = args.AppendParams("-n", flags.Namespace)
		}
		if flags.Output != "" {
			args = args.AppendParams("-o", string(flags.Output))
		}
	}

	return cli.commandRunner.Run(ctx, args)
}

func environ(values map[string]string) []string {
	env := []string{}
	for key, value := range values {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	return env
}
