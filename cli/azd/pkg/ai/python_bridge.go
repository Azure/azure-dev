package ai

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/azure/azure-dev/cli/azd/resources"
)

// ScriptPath is a type to represent the path of a Python script
type ScriptPath string

const (
	// PromptFlowClient is the path to the PromptFlow Client Python script
	PromptFlowClient ScriptPath = "pf_client.py"
	// MLClient is the path to the ML Client Python script
	MLClient ScriptPath = "ml_client.py"
)

// PythonBridge is an interface to execute python components from the embedded AI resources project
type PythonBridge interface {
	Initialize(ctx context.Context) error
	RequiredExternalTools(ctx context.Context) []tools.ExternalTool
	Run(ctx context.Context, scriptName ScriptPath, args ...string) (*exec.RunResult, error)
}

// pythonBridge is a bridge to execute python components from the embedded AI resources project
type pythonBridge struct {
	azdCtx      *azdcontext.AzdContext
	pythonCli   *python.PythonCli
	workingDir  string
	initialized bool
}

// NewPythonBridge creates a new PythonBridge instance
func NewPythonBridge(
	azdCtx *azdcontext.AzdContext,
	pythonCli *python.PythonCli,
) PythonBridge {
	return &pythonBridge{
		azdCtx:    azdCtx,
		pythonCli: pythonCli,
	}
}

// Initialize initializes the PythonBridge
// Copies embedded AI script files, creates a python virtual environment and installs the required dependencies
func (b *pythonBridge) Initialize(ctx context.Context) error {
	if b.initialized {
		return nil
	}

	if err := b.initPython(ctx); err != nil {
		return fmt.Errorf("failed initializing Python: %w", err)
	}

	b.initialized = true

	return nil
}

// RequiredExternalTools returns the required external tools for the PythonBridge
func (b *pythonBridge) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{b.pythonCli}
}

// Run executes the specified python script with the given arguments
func (b *pythonBridge) Run(ctx context.Context, scriptName ScriptPath, args ...string) (*exec.RunResult, error) {
	allArgs := append([]string{string(scriptName)}, args...)
	return b.pythonCli.Run(ctx, b.workingDir, ".venv", allArgs...)
}

// initPython initializes the Python environment
func (b *pythonBridge) initPython(ctx context.Context) error {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return err
	}

	targetDir := filepath.Join(configDir, "bin", "ai")
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		if err := os.MkdirAll(targetDir, osutil.PermissionDirectory); err != nil {
			return err
		}
	}

	if err := copyFS(resources.AiPythonApp, "ai-python", targetDir); err != nil {
		return err
	}

	if err := b.pythonCli.CreateVirtualEnv(ctx, targetDir, ".venv"); err != nil {
		return err
	}

	if err := b.pythonCli.InstallRequirements(ctx, targetDir, ".venv", "requirements.txt"); err != nil {
		return err
	}

	b.workingDir = targetDir

	return nil
}

// copyFS copies the contents of an embedded FS to a target directory
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
