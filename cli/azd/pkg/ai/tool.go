package ai

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/azure/azure-dev/cli/azd/resources"
)

type ScriptPath string

const (
	PromptFlowClient ScriptPath = "pf_client.py"
	MLClient         ScriptPath = "ml_client.py"
)

type Tool struct {
	azdCtx        *azdcontext.AzdContext
	env           *environment.Environment
	pythonCli     *python.PythonCli
	commandRunner exec.CommandRunner
	workingDir    string
	initialized   bool
}

func NewTool(
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	pythonCli *python.PythonCli,
	commandRunner exec.CommandRunner,
) *Tool {
	return &Tool{
		azdCtx:        azdCtx,
		env:           env,
		pythonCli:     pythonCli,
		commandRunner: commandRunner,
	}
}

func (t *Tool) Initialize(ctx context.Context) error {
	if t.initialized {
		return nil
	}

	if err := t.initPython(ctx); err != nil {
		return fmt.Errorf("failed initializing Python: %w", err)
	}

	t.initialized = true

	return nil
}

func (t *Tool) initPython(ctx context.Context) error {
	envDir := t.azdCtx.EnvironmentRoot(t.env.Name())
	targetDir := filepath.Join(envDir, "ai")
	if _, err := os.Stat(targetDir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(targetDir, osutil.PermissionDirectory); err != nil {
			return err
		}
	}

	if err := copyFS(resources.AiPythonApp, "ai-python", targetDir); err != nil {
		return err
	}

	if err := t.pythonCli.CreateVirtualEnv(ctx, targetDir, ".venv"); err != nil {
		return err
	}

	if err := t.pythonCli.InstallRequirements(ctx, targetDir, ".venv", "requirements.txt"); err != nil {
		return err
	}

	t.workingDir = targetDir

	return nil
}

func (t *Tool) Run(ctx context.Context, scriptName ScriptPath, args ...string) (*exec.RunResult, error) {
	allArgs := append([]string{string(scriptName)}, args...)
	runArgs := exec.
		NewRunArgs("python", allArgs...).
		WithCwd(t.workingDir)

	fmt.Printf("EXECUTING: python %s\n", strings.Join(allArgs, " "))

	result, err := t.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return nil, fmt.Errorf("failed running Python script: %w", err)
	}

	fmt.Printf("STDOUT: %s\n", result.Stdout)
	fmt.Printf("STDERR: %s\n", result.Stderr)

	return &result, nil
}

func copyFS(embedFs fs.FS, root string, target string) error {
	return fs.WalkDir(embedFs, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		targetPath := filepath.Join(target, name[len(root):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(embedFs, name)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}
