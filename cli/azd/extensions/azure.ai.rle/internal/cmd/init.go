// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleInitFlags struct {
	force bool
}

type initAction struct {
	cmd        *cobra.Command
	flags      *rleInitFlags
	folderName string
}

type rleSampleCatalog interface {
	SampleNames() []string
	Copy(sampleName string, folderName string, dest string, force bool) (string, error)
	Close() error
}

var loadRleSampleCatalogFunc = func() (rleSampleCatalog, error) {
	return project.LoadRleSampleCatalog()
}

var selectRleSampleFunc = selectRleSample

func newInitCommand() *cobra.Command {
	flags := &rleInitFlags{}

	cmd := &cobra.Command{
		Use:   "init <folder-name>",
		Short: "Initialize a local RLE environment from a sample",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&initAction{cmd: cmd, flags: flags, folderName: args[0]}).Run()
		},
	}

	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		var help strings.Builder
		help.WriteString("Initialize a local RLE environment from a sample\n")
		help.WriteString("Usage:\n")
		help.WriteString("  rle init <folder-name> [flags]\n")
		help.WriteString("Flags:\n")
		help.WriteString("      --force     Overwrite generated files in an existing non-empty session directory\n")
		help.WriteString("  -h, --help      help for init\n")
		if cmd.InheritedFlags().HasAvailableFlags() {
			help.WriteString("Global Flags:\n")
			help.WriteString(cmd.InheritedFlags().FlagUsages())
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), help.String())
	})
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite generated files in an existing non-empty session directory")
	return cmd
}

func (a *initAction) Run() error {
	folderName, err := project.ValidateEnvironmentName(a.folderName)
	if err != nil {
		return &azdext.LocalError{
			Message:    err.Error(),
			Code:       "rle_invalid_environment_name",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use snake_case starting with a letter, for example code_rl.",
		}
	}

	catalog, err := loadRleSampleCatalogFunc()
	if err != nil {
		return err
	}
	defer func() {
		_ = catalog.Close()
	}()
	sampleName, err := selectRleSampleFunc(a.cmd.Context(), catalog.SampleNames())
	if err != nil {
		return err
	}
	sessionDir, err := catalog.Copy(sampleName, folderName, ".", a.flags.force)
	if err != nil {
		return err
	}

	if err := saveRleStateIn(sessionDir, defaultRleState(folderName)); err != nil {
		return err
	}

	displayDir := "." + string(os.PathSeparator) + sessionDir
	if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "Copied RLE sample %q.\n", sampleName); err != nil {
		return err
	}
	_, err = fmt.Fprint(a.cmd.OutOrStdout(), initNextSteps(displayDir, runtime.GOOS, os.Getenv("SHELL")))
	return err
}

func selectRleSample(ctx context.Context, sampleNames []string) (string, error) {
	if len(sampleNames) == 0 {
		return "", &azdext.LocalError{
			Message:    "No RLE samples are available.",
			Code:       "rle_samples_empty",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Add a sample to the RLE samples repository, then retry.",
		}
	}
	choices := make([]*azdext.SelectChoice, len(sampleNames))
	for index, sampleName := range sampleNames {
		choices[index] = &azdext.SelectChoice{Label: sampleName, Value: sampleName}
	}
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", fmt.Errorf("create azd client for sample selection: %w", err)
	}
	defer azdClient.Close()
	response, err := azdClient.Prompt().Select(azdext.WithAccessToken(ctx), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         "Select an RLE sample",
			Choices:         choices,
			DisplayNumbers:  new(true),
			EnableFiltering: new(true),
		},
	})
	if err != nil {
		return "", fmt.Errorf("select RLE sample: %w", err)
	}
	selectedIndex := int(response.GetValue())
	if selectedIndex < 0 || selectedIndex >= len(sampleNames) {
		return "", fmt.Errorf("invalid RLE sample selection index: %d", selectedIndex)
	}
	return sampleNames[selectedIndex], nil
}

func initNextSteps(displayDir string, goos string, shell string) string {
	projectEndpoint := `https://<account>.services.ai.azure.com/api/projects/<project>`
	registryEndpoint := `<registry>.azurecr.io`
	setEnvironment := fmt.Sprintf(
		"  $env:FOUNDRY_PROJECT_ENDPOINT = %q\n  $env:AZURE_CONTAINER_REGISTRY_ENDPOINT = %q\n",
		projectEndpoint,
		registryEndpoint,
	)
	usePOSIXSyntax := goos != "windows"
	if isPowerShellExecutable(shell) {
		usePOSIXSyntax = false
	} else if isPOSIXShellExecutable(shell) {
		usePOSIXSyntax = true
	}
	if usePOSIXSyntax {
		setEnvironment = fmt.Sprintf(
			"  export FOUNDRY_PROJECT_ENDPOINT=%q\n  export AZURE_CONTAINER_REGISTRY_ENDPOINT=%q\n",
			projectEndpoint,
			registryEndpoint,
		)
	}

	return fmt.Sprintf(
		"Created RLE environment at: %s\n"+
			"\nRun locally:\n"+
			"  cd \"%s\"\n"+
			"  azd ai rle run\n"+
			"\nPublish to RLE when ready:\n"+
			"%s"+
			"  azd ai rle publish\n",
		displayDir,
		displayDir,
		setEnvironment,
	)
}

func isPowerShellExecutable(shell string) bool {
	switch executableName(shell) {
	case "pwsh", "pwsh.exe", "powershell", "powershell.exe":
		return true
	default:
		return false
	}
}

func isPOSIXShellExecutable(shell string) bool {
	switch executableName(shell) {
	case "sh", "sh.exe", "bash", "bash.exe", "zsh", "zsh.exe", "dash", "dash.exe", "fish", "fish.exe":
		return true
	default:
		return false
	}
}

func executableName(shell string) string {
	name := strings.ToLower(strings.TrimSpace(shell))
	if name == "" {
		return ""
	}
	for _, separator := range []string{`\`, `/`} {
		if index := strings.LastIndex(name, separator); index >= 0 {
			name = name[index+1:]
		}
	}
	return name
}
