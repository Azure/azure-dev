// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleInitFlags struct {
	force         bool
	rleType       string
	rleSubtype    string
	rleVersion    string
	agentName     string
	agentVersion  string
	baseURL       string
	harnessSource string
}

type initAction struct {
	cmd        *cobra.Command
	flags      *rleInitFlags
	folderName string
	noPrompt   bool
}

type rleInitTarget struct {
	rleType    project.RleType
	rleSubtype project.RleSubtype
}

var gymOpenEnvInitTarget = rleInitTarget{
	rleType:    project.RleTypeGym,
	rleSubtype: project.RleSubtypeOpenEnv,
}

// Harness scaffold sources: whether azd ai rle init should generate a
// generic, TODO-laden placeholder to wire up to a harness the caller has
// already built and deployed, or copy a fully-working sample (agent + rle)
// that runs end to end out of the box.
const (
	harnessSourceExisting = "existing"
	harnessSourceSample   = "sample"
)

type rleInitTargetOption struct {
	target rleInitTarget
	label  string
}

type rleSampleCatalog interface {
	SampleNames() []string
	Copy(sampleName string, folderName string, dest string, force bool) (string, error)
	Close() error
}

var loadRleSampleCatalogFunc = func() (rleSampleCatalog, error) {
	return project.LoadRleSampleCatalog(project.RleSampleCatalogOptions{
		ShowHiddenSamples: rleEnableAllEnabled(),
	})
}

// rleHarnessSample is satisfied by *project.RleHarnessSample; declared as an
// interface so tests can substitute a fake without touching the network.
type rleHarnessSample interface {
	Copy(folderName string, dest string, force bool) (string, error)
	Close() error
}

var loadRleHarnessSampleFunc = func(subtype project.RleSubtype) (rleHarnessSample, error) {
	return project.LoadRleHarnessSample(subtype)
}

var selectRleSampleFunc = selectRleSample

var selectRleInitTargetFunc = selectRleInitTarget

var selectHarnessSourceFunc = selectHarnessSource

var promptRleValueFunc = promptRleValue

var createRleHarnessScaffoldFunc = project.CreateRleHarnessScaffold

func newInitCommand(noPrompt *bool) *cobra.Command {
	flags := &rleInitFlags{}

	cmd := &cobra.Command{
		Use:   "init [folder-name]",
		Short: "Initialize a local RLE environment from a sample or harness scaffold",
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
		help.WriteString("Initialize a local RLE environment from a sample or harness scaffold\n")
		help.WriteString("Usage:\n")
		help.WriteString("  rle init [folder-name] [flags]\n")
		help.WriteString("Flags:\n")
		help.WriteString("      --agent-name string      HostedAgent name\n")
		help.WriteString("      --agent-version string   HostedAgent version\n")
		help.WriteString("      --base-url string        BYOH harness base URL\n")
		help.WriteString("      --force                  Overwrite generated files in an existing non-empty session directory\n")
		help.WriteString("      --harness-source string  Harness scaffold source: existing (placeholder to wire up to a harness " +
			"you already deployed) or sample (copy a full working agent+rle sample)\n")
		help.WriteString("      --rle-version string     RLE semantic version (defaults to 1.0.0 for a harness scaffold)\n")
		help.WriteString("      --subtype string         RLE control-plane subtype: OpenEnv, HostedAgent, or BYOH\n")
		help.WriteString("      --type string            RLE control-plane type: Gym or Harness\n")
		help.WriteString("  -h, --help                   help for init\n")
		if cmd.InheritedFlags().HasAvailableFlags() {
			help.WriteString("Global Flags:\n")
			help.WriteString(cmd.InheritedFlags().FlagUsages())
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), help.String())
	})
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite generated files in an existing non-empty session directory")
	cmd.Flags().StringVar(&flags.rleType, "type", "", "RLE control-plane type: Gym or Harness")
	cmd.Flags().StringVar(&flags.rleSubtype, "subtype", "", "RLE control-plane subtype: OpenEnv, HostedAgent, or BYOH")
	cmd.Flags().StringVar(&flags.rleVersion, "rle-version", "", "RLE semantic version")
	cmd.Flags().StringVar(&flags.agentName, "agent-name", "", "HostedAgent name")
	cmd.Flags().StringVar(&flags.agentVersion, "agent-version", "", "HostedAgent version")
	cmd.Flags().StringVar(&flags.baseURL, "base-url", "", "BYOH harness base URL")
	cmd.Flags().StringVar(
		&flags.harnessSource,
		"harness-source",
		"",
		"Harness scaffold source: existing (placeholder to wire up to a harness you already deployed) "+
			"or sample (copy a full working agent+rle sample)",
	)
	return cmd
}

func (a *initAction) Run() error {
	target, err := a.resolveInitTarget()
	if err != nil {
		return err
	}

	switch target {
	case gymOpenEnvInitTarget:
		return a.initializeGymOpenEnv(target)
	case rleInitTarget{rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent}:
		return a.initializeHostedAgent(target)
	case rleInitTarget{rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeBYOH}:
		return a.initializeBYOH(target)
	default:
		return fmt.Errorf("unsupported RLE init target %s/%s", target.rleType, target.rleSubtype)
	}
}

func (a *initAction) resolveInitTarget() (rleInitTarget, error) {
	harnessInitEnabled := rleEnableAllEnabled()
	typeValue := strings.TrimSpace(a.flags.rleType)
	subtypeValue := strings.TrimSpace(a.flags.rleSubtype)

	var target rleInitTarget
	var err error
	if typeValue == "" && subtypeValue == "" {
		if a.noPrompt {
			return gymOpenEnvInitTarget, nil
		}
		target, err = selectRleInitTargetFunc(a.cmd.Context(), harnessInitEnabled)
	} else {
		target, err = parseRleInitTarget(typeValue, subtypeValue)
	}
	if err != nil {
		return rleInitTarget{}, err
	}
	if isHarnessRleInitTarget(target) && !harnessInitEnabled {
		return rleInitTarget{}, &azdext.LocalError{
			Message:    fmt.Sprintf("RLE init target %s/%s is currently disabled.", target.rleType, target.rleSubtype),
			Code:       "rle_harness_init_disabled",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf("Set %s=true to enable harness RLE scaffolds.", rleEnableAllEnvVar),
		}
	}
	return target, nil
}

func (a *initAction) initializeGymOpenEnv(target rleInitTarget) error {
	if a.hasHarnessInputFlags() {
		return &azdext.LocalError{
			Message:    "Harness-specific flags can only be used with --type Harness and a harness --subtype.",
			Code:       "rle_harness_options_not_supported",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: `Remove the harness flags or select --type Harness with --subtype HostedAgent or BYOH.`,
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
	if strings.TrimSpace(a.flags.rleVersion) != "" {
		if _, err := normalizeInitRleVersion(a.flags.rleVersion); err != nil {
			return err
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

	config, err := project.LoadRleConfig(sessionDir)
	if err != nil {
		return err
	}
	config.Rle.Name = folderName
	config.Rle.Type = target.rleType
	config.Rle.Subtype = target.rleSubtype
	schemaVersion := project.CurrentRleManifestSchemaVersion
	config.SchemaVersion = &schemaVersion
	if strings.TrimSpace(a.flags.rleVersion) != "" {
		config.Rle.Version, err = normalizeInitRleVersion(a.flags.rleVersion)
		if err != nil {
			return err
		}
	}
	if err := project.WriteRleConfig(sessionDir, config); err != nil {
		return err
	}

	displayDir := "." + string(os.PathSeparator) + sessionDir
	if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "Copied RLE sample %q.\n", sampleName); err != nil {
		return err
	}
	_, err = fmt.Fprint(a.cmd.OutOrStdout(), initNextSteps(displayDir, runtime.GOOS, os.Getenv("SHELL")))
	return err
}

func (a *initAction) initializeHostedAgent(target rleInitTarget) error {
	harnessSource, err := a.resolveHarnessSource()
	if err != nil {
		return err
	}
	if strings.TrimSpace(a.flags.baseURL) != "" {
		return &azdext.LocalError{
			Message:    "--base-url can only be used with --type Harness --subtype BYOH.",
			Code:       "rle_harness_base_url_not_supported",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Remove --base-url or select --type Harness --subtype BYOH.",
		}
	}
	if harnessSource == harnessSourceSample {
		return a.createHarnessSampleScaffold(target)
	}

	agentName, err := a.resolveRequiredInput(
		a.flags.agentName,
		"Enter HostedAgent name",
		"An agent name is required for a HostedAgent RLE scaffold.",
		"rle_agent_name_required",
		"Provide --agent-name or run the command interactively.",
	)
	if err != nil {
		return err
	}
	agentVersion, err := a.resolveRequiredInput(
		a.flags.agentVersion,
		"Enter HostedAgent version",
		"An agent version is required for a HostedAgent RLE scaffold.",
		"rle_agent_version_required",
		"Provide --agent-version or run the command interactively.",
	)
	if err != nil {
		return err
	}

	folderName := a.folderName
	if folderName == "" {
		folderName = defaultRleFolderName(agentName)
	}
	return a.createHarnessScaffold(target, project.HarnessScaffoldOptions{
		EnvironmentName: folderName,
		RleVersion:      a.resolveRleVersion(),
		Type:            target.rleType,
		Subtype:         target.rleSubtype,
		AgentName:       agentName,
		AgentVersion:    agentVersion,
	})
}

func (a *initAction) initializeBYOH(target rleInitTarget) error {
	if strings.TrimSpace(a.flags.agentName) != "" || strings.TrimSpace(a.flags.agentVersion) != "" {
		return &azdext.LocalError{
			Message:    "--agent-name and --agent-version can only be used with --type Harness --subtype HostedAgent.",
			Code:       "rle_hosted_agent_options_not_supported",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Remove the HostedAgent flags or select --type Harness --subtype HostedAgent.",
		}
	}
	harnessSource, err := a.resolveHarnessSource()
	if err != nil {
		return err
	}
	if harnessSource == harnessSourceSample {
		return a.createHarnessSampleScaffold(target)
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
		"Enter BYOH harness base URL",
		"A BYOH harness base URL is required for a BYOH RLE scaffold.",
		"rle_harness_base_url_required",
		"Provide --base-url or run the command interactively.",
	)
	if err != nil {
		return err
	}
	return a.createHarnessScaffold(target, project.HarnessScaffoldOptions{
		EnvironmentName: folderName,
		RleVersion:      a.resolveRleVersion(),
		Type:            target.rleType,
		Subtype:         target.rleSubtype,
		BaseURL:         baseURL,
	})
}

func (a *initAction) createHarnessScaffold(target rleInitTarget, options project.HarnessScaffoldOptions) error {
	if _, err := normalizeInitRleVersion(options.RleVersion); err != nil {
		return err
	}
	sessionDir, err := createRleHarnessScaffoldFunc(options, ".", a.flags.force)
	if err != nil {
		return err
	}

	displayDir := "." + string(os.PathSeparator) + sessionDir
	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Created %s RLE scaffold.\n",
		rleInitTargetLabel(target),
	); err != nil {
		return err
	}
	_, err = fmt.Fprint(a.cmd.OutOrStdout(), initNextSteps(displayDir, runtime.GOOS, os.Getenv("SHELL")))
	return err
}

// createHarnessSampleScaffold copies the fully-working harness sample (agent
// + rle) for target.rleSubtype, instead of the generic placeholder
// createHarnessScaffold writes. --agent-name/--agent-version/--base-url are
// optional overrides here (the sample already ships working defaults for
// whichever of them apply to its subtype), unlike the existing-harness path,
// which requires them.
func (a *initAction) createHarnessSampleScaffold(target rleInitTarget) error {
	if strings.TrimSpace(a.flags.rleVersion) != "" {
		if _, err := normalizeInitRleVersion(a.flags.rleVersion); err != nil {
			return err
		}
	}

	folderName := a.folderName
	if folderName == "" {
		folderName = defaultRleHarnessSampleFolderName(target.rleSubtype)
	}
	folderName, err := validateRleFolderName(folderName)
	if err != nil {
		return err
	}

	sample, err := loadRleHarnessSampleFunc(target.rleSubtype)
	if err != nil {
		return err
	}
	defer func() {
		_ = sample.Close()
	}()
	sessionDir, err := sample.Copy(folderName, ".", a.flags.force)
	if err != nil {
		return err
	}

	rleDir := filepath.Join(sessionDir, "rle")
	config, err := project.LoadRleConfig(rleDir)
	if err != nil {
		return err
	}
	config.Rle.Name = folderName
	config.Rle.Type = target.rleType
	config.Rle.Subtype = target.rleSubtype
	schemaVersion := project.CurrentRleManifestSchemaVersion
	config.SchemaVersion = &schemaVersion
	if strings.TrimSpace(a.flags.rleVersion) != "" {
		config.Rle.Version, err = normalizeInitRleVersion(a.flags.rleVersion)
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(a.flags.agentName) != "" {
		agentName := a.flags.agentName
		config.Rle.AgentName = &agentName
	}
	if strings.TrimSpace(a.flags.agentVersion) != "" {
		agentVersion := a.flags.agentVersion
		config.Rle.AgentVersion = &agentVersion
	}
	if strings.TrimSpace(a.flags.baseURL) != "" {
		baseURL := a.flags.baseURL
		config.Rle.BaseURL = &baseURL
	}
	if err := project.WriteRleConfig(rleDir, config); err != nil {
		return err
	}

	displayDir := "." + string(os.PathSeparator) + sessionDir
	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Copied a working %s sample (agent + rle) to %s.\n",
		rleInitTargetLabel(target),
		displayDir,
	); err != nil {
		return err
	}
	_, err = fmt.Fprint(
		a.cmd.OutOrStdout(),
		initHarnessSampleNextSteps(displayDir, runtime.GOOS, os.Getenv("SHELL")),
	)
	return err
}

// resolveHarnessSource decides whether a Harness init should scaffold a
// placeholder for an existing/already-deployed harness, or copy a fully
// working sample. --no-prompt without an explicit --harness-source keeps the
// prior (placeholder) behavior so existing scripted callers are unaffected.
func (a *initAction) resolveHarnessSource() (string, error) {
	value := strings.TrimSpace(a.flags.harnessSource)
	if value != "" {
		switch value {
		case harnessSourceExisting, harnessSourceSample:
			return value, nil
		default:
			return "", &azdext.LocalError{
				Message:  fmt.Sprintf("Unsupported --harness-source %q.", value),
				Code:     "rle_harness_source_invalid",
				Category: azdext.LocalErrorCategoryUser,
				Suggestion: fmt.Sprintf(
					"Use --harness-source %s or --harness-source %s.",
					harnessSourceExisting,
					harnessSourceSample,
				),
			}
		}
	}
	if a.noPrompt {
		return harnessSourceExisting, nil
	}
	return selectHarnessSourceFunc(a.cmd.Context())
}

func defaultRleHarnessSampleFolderName(subtype project.RleSubtype) string {
	switch subtype {
	case project.RleSubtypeHostedAgent:
		return "hosted_agent_sample"
	case project.RleSubtypeBYOH:
		return "byoh_sample"
	default:
		return "harness_sample"
	}
}

func selectHarnessSource(ctx context.Context) (string, error) {
	choices := []*azdext.SelectChoice{
		{Label: "Point at an existing, already-deployed harness (placeholder scaffold)", Value: harnessSourceExisting},
		{Label: "Start from a full working sample (agent + rle)", Value: harnessSourceSample},
	}
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", fmt.Errorf("create azd client for harness source selection: %w", err)
	}
	defer azdClient.Close()
	response, err := azdClient.Prompt().Select(azdext.WithAccessToken(ctx), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         "Select a harness starting point",
			Choices:         choices,
			DisplayNumbers:  new(true),
			EnableFiltering: new(true),
		},
	})
	if err != nil {
		return "", fmt.Errorf("select harness source: %w", err)
	}
	selectedIndex := int(response.GetValue())
	if selectedIndex < 0 || selectedIndex >= len(choices) {
		return "", fmt.Errorf("invalid harness source selection index: %d", selectedIndex)
	}
	return choices[selectedIndex].Value, nil
}

func (a *initAction) resolveRleVersion() string {
	value := strings.TrimSpace(a.flags.rleVersion)
	if value == "" {
		return project.DefaultRleVersion
	}
	return value
}

func normalizeInitRleVersion(value string) (string, error) {
	return project.NormalizeRleVersion(value)
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

func (a *initAction) hasHarnessInputFlags() bool {
	return strings.TrimSpace(a.flags.agentName) != "" ||
		strings.TrimSpace(a.flags.agentVersion) != "" ||
		strings.TrimSpace(a.flags.baseURL) != ""
}

func rleInitTargetOptions(includeHarnessTypes bool) []rleInitTargetOption {
	options := []rleInitTargetOption{{
		target: gymOpenEnvInitTarget,
		label:  "Gym: OpenEnv",
	}}
	if includeHarnessTypes {
		options = append(options,
			rleInitTargetOption{
				target: rleInitTarget{
					rleType:    project.RleTypeHarness,
					rleSubtype: project.RleSubtypeHostedAgent,
				},
				label: "Harness: HostedAgent",
			},
			rleInitTargetOption{
				target: rleInitTarget{
					rleType:    project.RleTypeHarness,
					rleSubtype: project.RleSubtypeBYOH,
				},
				label: "Harness: BYOH",
			},
		)
	}
	return options
}

func selectRleInitTarget(ctx context.Context, includeHarnessTypes bool) (rleInitTarget, error) {
	options := rleInitTargetOptions(includeHarnessTypes)
	choices := make([]*azdext.SelectChoice, len(options))
	for index, option := range options {
		choices[index] = &azdext.SelectChoice{Label: option.label, Value: option.label}
	}
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return rleInitTarget{}, fmt.Errorf("create azd client for RLE type selection: %w", err)
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
		return rleInitTarget{}, fmt.Errorf("select RLE type: %w", err)
	}
	selectedIndex := int(response.GetValue())
	if selectedIndex < 0 || selectedIndex >= len(options) {
		return rleInitTarget{}, fmt.Errorf("invalid RLE type selection index: %d", selectedIndex)
	}
	return options[selectedIndex].target, nil
}

func parseRleInitTarget(typeValue string, subtypeValue string) (rleInitTarget, error) {
	if strings.TrimSpace(typeValue) == "" {
		return rleInitTarget{}, &azdext.LocalError{
			Message:    "--subtype requires --type.",
			Code:       "rle_init_type_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set --type Gym or --type Harness.",
		}
	}

	var rleType project.RleType
	switch strings.ToLower(strings.TrimSpace(typeValue)) {
	case "gym":
		rleType = project.RleTypeGym
	case "harness":
		rleType = project.RleTypeHarness
	default:
		return rleInitTarget{}, &azdext.LocalError{
			Message:    fmt.Sprintf("Unsupported RLE type %q.", typeValue),
			Code:       "rle_init_type_invalid",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use Gym or Harness.",
		}
	}

	if strings.TrimSpace(subtypeValue) == "" {
		if rleType == project.RleTypeGym {
			return gymOpenEnvInitTarget, nil
		}
		return rleInitTarget{}, &azdext.LocalError{
			Message:    "--subtype is required when --type is Harness.",
			Code:       "rle_init_subtype_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set --subtype HostedAgent or --subtype BYOH.",
		}
	}

	var subtype project.RleSubtype
	switch strings.ToLower(strings.TrimSpace(subtypeValue)) {
	case "openenv":
		subtype = project.RleSubtypeOpenEnv
	case "hostedagent":
		subtype = project.RleSubtypeHostedAgent
	case "byoh":
		subtype = project.RleSubtypeBYOH
	default:
		return rleInitTarget{}, &azdext.LocalError{
			Message:    fmt.Sprintf("Unsupported RLE subtype %q.", subtypeValue),
			Code:       "rle_init_subtype_invalid",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use OpenEnv, HostedAgent, or BYOH.",
		}
	}

	target := rleInitTarget{rleType: rleType, rleSubtype: subtype}
	if target == gymOpenEnvInitTarget ||
		target == (rleInitTarget{rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent}) ||
		target == (rleInitTarget{rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeBYOH}) {
		return target, nil
	}
	return rleInitTarget{}, &azdext.LocalError{
		Message:    fmt.Sprintf("RLE subtype %s is not supported for type %s.", subtype, rleType),
		Code:       "rle_init_type_configuration_invalid",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: `Use Gym: OpenEnv, Harness: HostedAgent, or Harness: BYOH.`,
	}
}

func isHarnessRleInitTarget(target rleInitTarget) bool {
	return target.rleType == project.RleTypeHarness
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

func rleInitTargetLabel(target rleInitTarget) string {
	switch target {
	case rleInitTarget{rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent}:
		return "HostedAgent"
	case rleInitTarget{rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeBYOH}:
		return "BYOH"
	default:
		return string(target.rleSubtype)
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
	setEnvironment := rleEnvironmentSetupSnippet(goos, shell)
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

// initHarnessSampleNextSteps is initNextSteps' counterpart for
// createHarnessSampleScaffold: the copied sample's RLE side lives in a rle/
// subfolder (what azd ai rle run/publish need to be run from), and its agent
// side lives in a sibling agent/ folder that must be built and deployed
// separately -- there's no rle.toml for run/publish to act on there.
func initHarnessSampleNextSteps(displayDir string, goos string, shell string) string {
	rleDir := filepath.Join(displayDir, "rle")
	agentDir := filepath.Join(displayDir, "agent")
	setEnvironment := rleEnvironmentSetupSnippet(goos, shell)
	return fmt.Sprintf(
		"Copied a working sample to: %s\n"+
			"  %s  (the harness/agent implementation -- build and deploy this yourself)\n"+
			"  %s  (the RLE wrapper -- this is what azd ai rle acts on)\n"+
			"\nRun the RLE side locally:\n"+
			"  cd \"%s\"\n"+
			"  azd ai rle run\n"+
			"\nPublish to RLE when ready:\n"+
			"%s"+
			"  azd ai rle publish\n"+
			"\nBefore publishing, update %s/rle.toml's baseUrl/agentName/agentVersion "+
			"to point at your own deployment of %s, once it is live.\n",
		displayDir,
		agentDir,
		rleDir,
		rleDir,
		setEnvironment,
		rleDir,
		agentDir,
	)
}

func rleEnvironmentSetupSnippet(goos string, shell string) string {
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
	return setEnvironment
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
