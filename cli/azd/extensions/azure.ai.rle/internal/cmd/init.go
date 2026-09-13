// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleInitFlags struct {
	force        bool
	initType     string
	agentName    string
	agentVersion string
	baseURL      string
}

type initAction struct {
	cmd        *cobra.Command
	flags      *rleInitFlags
	folderName string
	noPrompt   bool
}

type rleInitType string

const (
	rleInitTypeGymOpenEnv  rleInitType = "gym-openenv"
	rleInitTypeHostedAgent rleInitType = "hosted-agent"
	rleInitTypeBYOH        rleInitType = "byoh"
)

type rleInitTypeOption struct {
	initType rleInitType
	label    string
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

var selectRleInitTypeFunc = selectRleInitType

var promptRleValueFunc = promptRleValue

var createRleAgentScaffoldFunc = project.CreateRleAgentScaffold

func newInitCommand(noPrompt *bool) *cobra.Command {
	flags := &rleInitFlags{}

	cmd := &cobra.Command{
		Use:   "init [folder-name]",
		Short: "Initialize a local RLE environment from a sample or agent scaffold",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			folderName := ""
			if len(args) == 1 {
				folderName = args[0]
			}
			return (&initAction{
				cmd:        cmd,
				flags:      flags,
				folderName: folderName,
				noPrompt:   noPrompt != nil && *noPrompt,
			}).Run()
		},
	}

	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		var help strings.Builder
		help.WriteString("Initialize a local RLE environment from a sample or agent scaffold\n")
		help.WriteString("Usage:\n")
		help.WriteString("  rle init [folder-name] [flags]\n")
		help.WriteString("Flags:\n")
		help.WriteString("      --agent-name string      Hosted Agent name\n")
		help.WriteString("      --agent-version string   Hosted Agent version\n")
		help.WriteString("      --base-url string        BYOH agent base URL\n")
		help.WriteString("      --force                  Overwrite generated files in an existing non-empty session directory\n")
		help.WriteString("      --type string            RLE target type: gym-openenv, hosted-agent, or byoh\n")
		help.WriteString("  -h, --help                   help for init\n")
		if cmd.InheritedFlags().HasAvailableFlags() {
			help.WriteString("Global Flags:\n")
			help.WriteString(cmd.InheritedFlags().FlagUsages())
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), help.String())
	})
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite generated files in an existing non-empty session directory")
	cmd.Flags().StringVar(&flags.initType, "type", "", "RLE target type: gym-openenv, hosted-agent, or byoh")
	cmd.Flags().StringVar(&flags.agentName, "agent-name", "", "Hosted Agent name")
	cmd.Flags().StringVar(&flags.agentVersion, "agent-version", "", "Hosted Agent version")
	cmd.Flags().StringVar(&flags.baseURL, "base-url", "", "BYOH agent base URL")
	return cmd
}

func (a *initAction) Run() error {
	initType, err := a.resolveInitType()
	if err != nil {
		return err
	}

	switch initType {
	case rleInitTypeGymOpenEnv:
		return a.initializeGymOpenEnv()
	case rleInitTypeHostedAgent:
		return a.initializeHostedAgent()
	case rleInitTypeBYOH:
		return a.initializeBYOH()
	default:
		return fmt.Errorf("unsupported RLE init type %q", initType)
	}
}

func (a *initAction) resolveInitType() (rleInitType, error) {
	agentInitEnabled := rleAgentInitEnabled()
	var initType rleInitType
	var err error
	if strings.TrimSpace(a.flags.initType) == "" {
		if a.noPrompt {
			return rleInitTypeGymOpenEnv, nil
		}
		initType, err = selectRleInitTypeFunc(a.cmd.Context(), agentInitEnabled)
	} else {
		initType, err = parseRleInitType(a.flags.initType)
	}
	if err != nil {
		return "", err
	}
	if isAgentRleInitType(initType) && !agentInitEnabled {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("RLE init type %q is currently disabled.", initType),
			Code:       "rle_agent_init_disabled",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf("Set %s=true to enable agent RLE scaffolds.", rleAgentInitEnableEnvVar),
		}
	}
	return initType, nil
}

func (a *initAction) initializeGymOpenEnv() error {
	if a.hasAgentInputFlags() {
		return &azdext.LocalError{
			Message:    "Agent-specific flags can only be used with --type hosted-agent or --type byoh.",
			Code:       "rle_agent_options_not_supported",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Remove the agent flags or select an agent RLE type.",
		}
	}
	if a.noPrompt && a.folderName == "" {
		return &azdext.LocalError{
			Message:    "A sample name is required when prompts are disabled.",
			Code:       "rle_sample_name_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle init <sample-name> --no-prompt.",
		}
	}
	folderName := a.folderName
	if folderName != "" {
		var err error
		folderName, err = validateRleFolderName(folderName)
		if err != nil {
			return err
		}
	}

	catalog, err := loadRleSampleCatalogFunc()
	if err != nil {
		return err
	}
	defer func() {
		_ = catalog.Close()
	}()
	requestedSample := ""
	if a.noPrompt {
		requestedSample = folderName
	}
	sampleName, err := resolveRleSample(a.cmd.Context(), requestedSample, catalog.SampleNames())
	if err != nil {
		return err
	}
	if folderName == "" {
		folderName, err = validateRleFolderName(sampleName)
		if err != nil {
			return err
		}
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

func (a *initAction) initializeHostedAgent() error {
	agentName, err := a.resolveRequiredInput(
		a.flags.agentName,
		"Enter Hosted Agent name",
		"An agent name is required for a Hosted Agent RLE scaffold.",
		"rle_agent_name_required",
		"Provide --agent-name or run the command interactively.",
	)
	if err != nil {
		return err
	}
	agentVersion, err := a.resolveRequiredInput(
		a.flags.agentVersion,
		"Enter Hosted Agent version",
		"An agent version is required for a Hosted Agent RLE scaffold.",
		"rle_agent_version_required",
		"Provide --agent-version or run the command interactively.",
	)
	if err != nil {
		return err
	}
	if strings.TrimSpace(a.flags.baseURL) != "" {
		return &azdext.LocalError{
			Message:    "--base-url can only be used with --type byoh.",
			Code:       "rle_base_url_not_supported",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Remove --base-url or select --type byoh.",
		}
	}

	folderName := a.folderName
	if folderName == "" {
		folderName = defaultRleFolderName(agentName)
	}
	return a.createAgentScaffold(project.AgentScaffoldOptions{
		Kind:            project.AgentScaffoldKindHostedAgent,
		EnvironmentName: folderName,
		AgentName:       agentName,
		AgentVersion:    agentVersion,
	})
}

func (a *initAction) initializeBYOH() error {
	if strings.TrimSpace(a.flags.agentName) != "" || strings.TrimSpace(a.flags.agentVersion) != "" {
		return &azdext.LocalError{
			Message:    "--agent-name and --agent-version can only be used with --type hosted-agent.",
			Code:       "rle_hosted_agent_options_not_supported",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Remove the Hosted Agent flags or select --type hosted-agent.",
		}
	}

	folderName := a.folderName
	if folderName == "" {
		var err error
		folderName, err = a.resolveRequiredInput(
			"",
			"Enter RLE environment name",
			"An RLE environment name is required for a BYOH scaffold.",
			"rle_environment_name_required",
			"Provide a folder name or run the command interactively.",
		)
		if err != nil {
			return err
		}
	}
	baseURL, err := a.resolveRequiredInput(
		a.flags.baseURL,
		"Enter BYOH agent base URL",
		"A BYOH agent base URL is required for a BYOH RLE scaffold.",
		"rle_agent_base_url_required",
		"Provide --base-url or run the command interactively.",
	)
	if err != nil {
		return err
	}
	return a.createAgentScaffold(project.AgentScaffoldOptions{
		Kind:            project.AgentScaffoldKindBYOH,
		EnvironmentName: folderName,
		BaseURL:         baseURL,
	})
}

func (a *initAction) createAgentScaffold(options project.AgentScaffoldOptions) error {
	folderName, err := validateRleFolderName(options.EnvironmentName)
	if err != nil {
		return err
	}
	options.EnvironmentName = folderName
	sessionDir, err := createRleAgentScaffoldFunc(options, ".", a.flags.force)
	if err != nil {
		return err
	}
	if err := saveRleStateIn(sessionDir, defaultRleState(folderName)); err != nil {
		return err
	}

	displayDir := "." + string(os.PathSeparator) + sessionDir
	if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "Created %s RLE scaffold.\n", rleInitTypeLabel(options.Kind)); err != nil {
		return err
	}
	_, err = fmt.Fprint(a.cmd.OutOrStdout(), initNextSteps(displayDir, runtime.GOOS, os.Getenv("SHELL")))
	return err
}

func (a *initAction) resolveRequiredInput(
	value string,
	message string,
	errorMessage string,
	errorCode string,
	suggestion string,
) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && !a.noPrompt {
		var err error
		value, err = promptRleValueFunc(a.cmd.Context(), message)
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
	}
	if value == "" {
		return "", &azdext.LocalError{
			Message:    errorMessage,
			Code:       errorCode,
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: suggestion,
		}
	}
	return value, nil
}

func (a *initAction) hasAgentInputFlags() bool {
	return strings.TrimSpace(a.flags.agentName) != "" ||
		strings.TrimSpace(a.flags.agentVersion) != "" ||
		strings.TrimSpace(a.flags.baseURL) != ""
}

func rleInitTypeOptions(includeAgentTypes bool) []rleInitTypeOption {
	options := []rleInitTypeOption{{
		initType: rleInitTypeGymOpenEnv,
		label:    "Gym, OpenEnv",
	}}
	if includeAgentTypes {
		options = append(options,
			rleInitTypeOption{
				initType: rleInitTypeHostedAgent,
				label:    "Agent, Hosted Agent",
			},
			rleInitTypeOption{
				initType: rleInitTypeBYOH,
				label:    "Agent, BYOH",
			},
		)
	}
	return options
}

func selectRleInitType(ctx context.Context, includeAgentTypes bool) (rleInitType, error) {
	options := rleInitTypeOptions(includeAgentTypes)
	choices := make([]*azdext.SelectChoice, len(options))
	for index, option := range options {
		choices[index] = &azdext.SelectChoice{Label: option.label, Value: string(option.initType)}
	}
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", fmt.Errorf("create azd client for RLE type selection: %w", err)
	}
	defer azdClient.Close()
	response, err := azdClient.Prompt().Select(azdext.WithAccessToken(ctx), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         "Select an RLE type",
			Choices:         choices,
			DisplayNumbers:  new(true),
			EnableFiltering: new(true),
		},
	})
	if err != nil {
		return "", fmt.Errorf("select RLE type: %w", err)
	}
	selectedIndex := int(response.GetValue())
	if selectedIndex < 0 || selectedIndex >= len(options) {
		return "", fmt.Errorf("invalid RLE type selection index: %d", selectedIndex)
	}
	return options[selectedIndex].initType, nil
}

func parseRleInitType(value string) (rleInitType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gym", "openenv", "gym-openenv":
		return rleInitTypeGymOpenEnv, nil
	case "hosted-agent", "hostedagent":
		return rleInitTypeHostedAgent, nil
	case "byoh":
		return rleInitTypeBYOH, nil
	default:
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Unsupported RLE init type %q.", value),
			Code:       "rle_init_type_invalid",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use gym-openenv, hosted-agent, or byoh.",
		}
	}
}

func isAgentRleInitType(initType rleInitType) bool {
	return initType == rleInitTypeHostedAgent || initType == rleInitTypeBYOH
}

func defaultRleFolderName(agentName string) string {
	folderName := strings.ReplaceAll(project.Slug(agentName), "-", "_")
	if folderName == "" {
		return "agent_rle"
	}
	if folderName[0] >= '0' && folderName[0] <= '9' {
		return "agent_" + folderName
	}
	return folderName
}

func rleInitTypeLabel(kind project.AgentScaffoldKind) string {
	switch kind {
	case project.AgentScaffoldKindHostedAgent:
		return "Hosted Agent"
	case project.AgentScaffoldKindBYOH:
		return "BYOH"
	default:
		return string(kind)
	}
}

func promptRleValue(ctx context.Context, message string) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", fmt.Errorf("create azd client for RLE input: %w", err)
	}
	defer azdClient.Close()
	response, err := azdClient.Prompt().Prompt(azdext.WithAccessToken(ctx), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        message,
			IgnoreHintKeys: true,
		},
	})
	if err != nil {
		return "", fmt.Errorf("prompt for RLE input: %w", err)
	}
	if response == nil {
		return "", fmt.Errorf("prompt for RLE input returned no response")
	}
	return response.GetValue(), nil
}

func validateRleFolderName(folderName string) (string, error) {
	validated, err := project.ValidateEnvironmentName(folderName)
	if err == nil {
		return validated, nil
	}
	return "", &azdext.LocalError{
		Message:    err.Error(),
		Code:       "rle_invalid_environment_name",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Use snake_case starting with a letter, for example code_rl.",
	}
}

func resolveRleSample(ctx context.Context, requestedSample string, sampleNames []string) (string, error) {
	if requestedSample == "" {
		return selectRleSampleFunc(ctx, sampleNames)
	}
	if slices.Contains(sampleNames, requestedSample) {
		return requestedSample, nil
	}
	return "", &azdext.LocalError{
		Message:    fmt.Sprintf("RLE sample %q was not found.", requestedSample),
		Code:       "rle_sample_not_found",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf("Choose one of the available samples: %s.", strings.Join(sampleNames, ", ")),
	}
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
