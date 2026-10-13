// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"azure.ai.rle/internal/project"
	"azure.ai.rle/internal/rollouts"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// rolloutFlags holds the CLI-facing configuration for one rollout.
type rolloutFlags struct {
	mode       string
	loom       bool
	backend    string
	model      string
	loraRank   int
	task       string
	taskFile   string
	rolloutID  string
	renderer   string
	sequenceID int
	timeout    int
	outputDir  string
}

type rolloutAction struct {
	cmd   *cobra.Command
	flags *rolloutFlags
	// monitor opens the local dashboard after the rollout. It is cleared when
	// the dashboard cannot open safely, so the rollout warns instead of failing.
	monitor bool
}

// rolloutTarget identifies the exact published environment version to run, and the
// Foundry project that owns it.
type rolloutTarget struct {
	environmentName string
	projectEndpoint string
	version         string
	isGymOpenEnv    bool
}

func newRolloutCommand() *cobra.Command {
	flags := &rolloutFlags{
		mode:      rolloutModeTrain,
		loraRank:  16,
		timeout:   600,
		outputDir: defaultRolloutOutputDir,
	}

	cmd := &cobra.Command{
		Use:   "rollout [environment-name]",
		Short: "Execute one rollout of a published RLE environment",
		Long: `Execute one rollout of a published RLE environment.

rollout provisions everything a Loom-backed rollout needs and tears it down again: it
creates a real Loom training session for --model, saves a sampler checkpoint, calls RLE's
Execute Rollout API with your task, prints the live execution milestones followed by the
resulting reward and trajectory summary, then closes the Loom session. You never handle
Loom session or checkpoint identifiers directly.

--mode train (the default) always uses Loom, because only Loom returns the token ids and
logprobs training needs. --mode eval instead samples a model deployment in the Foundry project
(--model names the deployment) and creates no Loom session. Foundry rollouts are text-level:
they carry no token ids and are never trainable. Add --loom to run the eval on a Loom session
instead. --lora-rank, --sequence-id, --renderer and --timeout configure Loom and are rejected
for a Foundry eval.

The response also carries the full capture graph — token ids, logprobs and loss masks —
which is too large to print and cannot be fetched again once the rollout returns. It is
written to .output/<rollout-id>/ (see --output-dir) and the files are listed on completion.

rollout must be run from a folder containing rle.toml. It uses rle.name
and rle.version from that file.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&rolloutAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(
		&flags.mode,
		"mode",
		flags.mode,
		"What the rollout is for: \"train\" (default, always a Loom session) or "+
			"\"eval\" (sample a Foundry model deployment by default).",
	)
	cmd.Flags().BoolVar(
		&flags.loom,
		"loom",
		false,
		"With --mode eval, run on a Loom session instead of a Foundry deployment. Train always uses Loom.",
	)
	cmd.Flags().StringVar(
		&flags.model,
		"model",
		"",
		"Model to sample: a Loom base model name such as Qwen/Qwen3-32B, or, for a Foundry eval, "+
			"the name of a model deployment in the project. Required unless rle.toml sets model.name.",
	)
	cmd.Flags().IntVar(&flags.loraRank, "lora-rank", flags.loraRank, "LoRA adapter rank for the Loom session.")
	cmd.Flags().StringVar(&flags.task, "task", "", "Inline JSON task payload for the sandbox reset operation.")
	cmd.Flags().StringVar(&flags.taskFile, "task-file", "", "Path to a JSON file with the task payload.")
	cmd.Flags().StringVar(
		&flags.rolloutID,
		"rollout-id",
		"",
		"Caller-generated rollout correlation id. Defaults to a generated GUID.",
	)
	cmd.Flags().StringVar(
		&flags.renderer,
		"renderer",
		"",
		"Capture Proxy renderer name. Defaults to a service-selected renderer when omitted.",
	)
	cmd.Flags().IntVar(&flags.sequenceID, "sequence-id", 0, "Loom training-step sequence id for this rollout.")
	cmd.Flags().IntVar(&flags.timeout, "timeout", flags.timeout, "Loom session provisioning timeout in seconds.")
	cmd.Flags().StringVar(
		&flags.outputDir,
		"output-dir",
		flags.outputDir,
		"Directory to write this rollout's artifacts under, as <output-dir>/<rollout-id>/. "+
			"The Execute Rollout response carries the full capture graph — token ids, logprobs "+
			"and loss masks — which is too large to print and is not retrievable afterwards.",
	)
	cmd.Long += `

Rollout opens a local dashboard from the rollout artifacts after execution
resources are released; it stays running until Ctrl+C. The dashboard is skipped
when --output is set.
Reopen a saved result with azd ai rle monitor --rollout-id <id> [--output-dir <directory>].`
	return cmd
}

func (a *rolloutAction) Run() error {
	a.monitor = true
	// The monitor yields to machine-readable output rather than rejecting it.
	if flag := a.cmd.Flag("output"); flag != nil && flag.Changed {
		a.monitor = false
	}
	outputDir, err := resolveRolloutOutputDir(a.flags.outputDir)
	if err != nil {
		return err
	}
	a.flags.outputDir = outputDir
	if err := a.validateBackend(); err != nil {
		return err
	}
	target, rle, err := a.resolveTarget()
	if err != nil {
		return err
	}

	model := strings.TrimSpace(a.flags.model)
	if model == "" {
		model = defaultModelFromRleConfig()
	}
	if model == "" {
		return &azdext.LocalError{
			Message:  "--model is required to choose the model a rollout samples.",
			Code:     "rle_rollout_model_required",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: "Pass --model with a Loom base model name (for example --model Qwen/Qwen3-32B) " +
				"or, for a Foundry eval, a model deployment name, " +
				"or set model.name in rle.toml.",
		}
	}

	task, err := readJSONFlagOrFile(
		"--task", a.flags.task, "--task-file", a.flags.taskFile, target.isGymOpenEnv,
	)
	if err != nil {
		return err
	}

	rolloutID := strings.TrimSpace(a.flags.rolloutID)
	if a.cmd.Flags().Changed("rollout-id") && rolloutID == "" {
		return invalidMonitorIDError(fmt.Errorf("--rollout-id requires a non-empty rollout ID"))
	}
	if rolloutID == "" {
		rolloutID, err = newRolloutID()
		if err != nil {
			return err
		}
	}
	if err := rollouts.ValidateID(rolloutID); err != nil {
		return invalidMonitorIDError(err)
	}
	ctx, stopSignals := signal.NotifyContext(a.cmd.Context(), os.Interrupt)
	defer stopSignals()

	if err := a.executeAndSave(ctx, target, rle, model, task, rolloutID); err != nil {
		return err
	}
	if a.monitor {
		reader := &rollouts.ArtifactReader{OutputDir: outputDir}
		return runRolloutMonitor(ctx, reader, rolloutID, false, a.cmd.OutOrStdout(), a.cmd.ErrOrStderr())
	}
	return nil
}

func (a *rolloutAction) executeAndSave(
	ctx context.Context,
	target rolloutTarget,
	rle *rleClient,
	model string,
	task json.RawMessage,
	rolloutID string,
) (err error) {
	loom, err := createLoomSessionClient(target.projectEndpoint)
	if err != nil {
		return err
	}
	out := a.cmd.OutOrStdout()
	errOut := a.cmd.ErrOrStderr()

	var policy *rolloutPolicy
	if a.flags.backend == foundryPolicyType {
		// A hosted deployment has no weights to pin, so there is no session or checkpoint to create.
		policy = &rolloutPolicy{
			Type:            foundryPolicyType,
			DeploymentName:  model,
			ProjectEndpoint: target.projectEndpoint,
		}
	} else {
		var release func(*error)
		policy, release, err = a.provisionLoomPolicy(ctx, loom, target, model, rolloutID)
		if release != nil {
			defer release(&err)
		}
		if err != nil {
			return err
		}
	}

	loomToken, err := loom.bearerToken(ctx)
	if err != nil {
		return fmt.Errorf("authenticate to Loom for Execute Rollout: %w", err)
	}

	var sampling *rolloutSamplingOptions
	if a.flags.renderer != "" {
		sampling = &rolloutSamplingOptions{RendererName: a.flags.renderer}
	}
	if _, err := fmt.Fprintf(
		out,
		"Executing rollout %s for environment %s version %s ...\n",
		rolloutID,
		target.environmentName,
		target.version,
	); err != nil {
		return err
	}
	progress := newExecuteRolloutProgressRenderer(out)
	response, err := rle.executeRollout(ctx, target.environmentName, target.version, loomToken, executeRolloutRequest{
		RolloutID: rolloutID,
		Task:      task,
		Policy:    policy,
		Sampling:  sampling,
	}, progress.Render, errOut)
	if err != nil {
		if isRleNotFound(err) {
			return environmentVersionNotFoundError(target.environmentName, target.version)
		}
		return serviceError(err)
	}

	if response.RolloutID != rolloutID {
		return fmt.Errorf("execute response rollout ID does not match the requested ID")
	}
	if err := printRolloutResult(out, response); err != nil {
		return err
	}

	artifacts, err := writeRolloutArtifacts(a.flags.outputDir, response, &rollouts.ArtifactMetadata{
		Version: rollouts.ArtifactVersion, SavedAt: time.Now().UTC(), ProjectEndpoint: target.projectEndpoint,
		Environment: &rollouts.Environment{Name: target.environmentName, Version: target.version},
	})
	if err != nil {
		_, writeErr := fmt.Fprintf(errOut, "\nWarning: could not write rollout artifacts: %v\n", err)
		return errors.Join(writeErr, a.skipMonitor(errOut))
	}
	return printRolloutArtifacts(out, artifacts)
}

// provisionLoomPolicy creates the Loom session and sampler checkpoint a Loom rollout is bound to.
// The returned release closes the session and folds any failure into the rollout's error; it is
// non-nil as soon as the session exists, even when saving the checkpoint then fails.
func (a *rolloutAction) provisionLoomPolicy(
	ctx context.Context,
	loom *loomSessionClient,
	target rolloutTarget,
	model string,
	rolloutID string,
) (*rolloutPolicy, func(*error), error) {
	out := a.cmd.OutOrStdout()
	errOut := a.cmd.ErrOrStderr()
	timeout := time.Duration(a.flags.timeout) * time.Second

	if _, err := fmt.Fprintf(out, "Creating Loom training session for model %s ...\n", model); err != nil {
		return nil, nil, err
	}
	sessionID, err := loom.createSession(ctx, model, a.flags.loraRank, timeout)
	if err != nil {
		return nil, nil, loomServiceErrorFor("create Loom training session", err)
	}
	release := func(errp *error) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if cerr := loom.closeSession(cctx, sessionID); cerr != nil {
			_, writeErr := fmt.Fprintf(errOut, "Warning: failed to close Loom session; it may remain allocated: %v\n", cerr)
			*errp = errors.Join(*errp, writeErr, a.skipMonitor(errOut))
			return
		}
		_, writeErr := fmt.Fprintln(errOut, "Loom session closed.")
		*errp = errors.Join(*errp, writeErr)
	}

	checkpointName := fmt.Sprintf("azd-rollout-%s", rolloutID[:8])
	if _, err := fmt.Fprintln(out, "Saving Loom sampler checkpoint ..."); err != nil {
		return nil, release, err
	}
	checkpointID, err := loom.saveWeightsForSampler(ctx, sessionID, a.flags.sequenceID, checkpointName, timeout)
	if err != nil {
		return nil, release, loomServiceErrorFor("save Loom sampler checkpoint", err)
	}

	sequenceID := int64(a.flags.sequenceID)
	return &rolloutPolicy{
		Type:            loomPolicyType,
		ModelName:       model,
		ProjectEndpoint: target.projectEndpoint,
		SessionID:       sessionID,
		CheckpointID:    checkpointID,
		SequenceID:      &sequenceID,
	}, release, nil
}

// validateBackend resolves which backend the mode selects and rejects flags that cannot take
// effect on it, rather than accepting and ignoring them.
func (a *rolloutAction) validateBackend() error {
	switch a.flags.mode {
	case rolloutModeTrain:
		if a.cmd.Flags().Changed("loom") {
			return &azdext.LocalError{
				Message:    "--loom has no effect with --mode train, which always uses Loom.",
				Code:       "rle_rollout_mode_flag_unsupported",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Remove --loom, or use --mode eval --loom.",
			}
		}
		a.flags.backend = loomPolicyType
		return nil
	case rolloutModeEval:
	default:
		return &azdext.LocalError{
			Message:    fmt.Sprintf("--mode %q is not supported.", a.flags.mode),
			Code:       "rle_rollout_mode_invalid",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use --mode eval or --mode train.",
		}
	}

	if a.flags.loom {
		a.flags.backend = loomPolicyType
		return nil
	}
	a.flags.backend = foundryPolicyType
	for _, name := range []string{"lora-rank", "sequence-id", "renderer", "timeout"} {
		if a.cmd.Flags().Changed(name) {
			return &azdext.LocalError{
				Message:    fmt.Sprintf("--%s configures a Loom session and is not supported for a Foundry eval.", name),
				Code:       "rle_rollout_backend_flag_unsupported",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Remove the flag, or add --loom to run the eval on Loom.",
			}
		}
	}
	return nil
}

// skipMonitor turns off a monitor that can no longer open safely, and tells the user why no
// dashboard appears. It is a no-op when the monitor is off.
func (a *rolloutAction) skipMonitor(errOut io.Writer) error {
	if !a.monitor {
		return nil
	}
	a.monitor = false
	_, err := fmt.Fprintln(errOut, "The rollout monitor was not opened.")
	return err
}

func (a *rolloutAction) resolveTarget() (rolloutTarget, *rleClient, error) {
	localConfig, err := project.LoadRleConfig(".")
	if err != nil {
		return rolloutTarget{}, nil, err
	}
	environmentName := localConfig.Rle.Name
	version, err := project.NormalizeRleVersion(localConfig.Rle.Version)
	if err != nil {
		return rolloutTarget{}, nil, err
	}
	projectEndpoint, err := resolveEnvironmentListProjectEndpoint()
	if err != nil {
		return rolloutTarget{}, nil, err
	}
	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return rolloutTarget{}, nil, err
	}
	return rolloutTarget{
		environmentName: environmentName,
		projectEndpoint: projectEndpoint,
		version:         version,
		isGymOpenEnv:    isGymOpenEnv(localConfig.Harness),
	}, client, nil
}

func isGymOpenEnv(harness project.RleHarnessManifest) bool {
	return harness.Type == project.RleTypeGym && harness.Subtype == project.RleSubtypeOpenEnv
}

// defaultModelFromRleConfig best-effort loads rle.toml from the current folder and returns
// model.name, so --model can be omitted when the manifest already declares one.
// Any load error (including no rle.toml present) is treated as "no default available"
// rather than a hard failure, since --model is only required when no default exists.
func defaultModelFromRleConfig() string {
	config, err := project.LoadRleConfig(".")
	if err != nil || config.Model == nil || config.Model.Name == nil {
		return ""
	}
	return strings.TrimSpace(*config.Model.Name)
}

// readJSONFlagOrFile reads a JSON payload from an inline flag or a file flag (at most one
// may be set) and validates it parses as JSON. Returns nil if neither is set.
func readJSONFlagOrFile(
	inlineName, inline, fileName, file string,
	allowWindowsPowerShellRepair bool,
) (json.RawMessage, error) {
	inline = strings.TrimSpace(inline)
	file = strings.TrimSpace(file)
	if inline != "" && file != "" {
		return nil, &azdext.LocalError{
			Message:  fmt.Sprintf("%s and %s are mutually exclusive.", inlineName, fileName),
			Code:     "rle_rollout_conflicting_payload_flags",
			Category: azdext.LocalErrorCategoryUser,
		}
	}

	var raw []byte
	switch {
	case inline != "":
		raw = []byte(inline)
	case file != "":
		// #nosec G304 -- reading the user-selected payload file is the purpose of this option.
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", fileName, err)
		}
		raw = data
	default:
		return nil, nil
	}

	payloadName := inlineName
	if file != "" {
		payloadName = fileName
	}

	var probe any
	if err := json.Unmarshal(raw, &probe); err != nil {
		if allowWindowsPowerShellRepair && inline != "" && runtime.GOOS == "windows" {
			if repaired, ok := repairWindowsPowerShellJSONObject(raw); ok {
				raw = repaired
				if err := json.Unmarshal(raw, &probe); err == nil {
					return json.RawMessage(raw), nil
				}
			}
		}
		return nil, &azdext.LocalError{
			Message:  fmt.Sprintf("%s must contain valid JSON: %v", payloadName, err),
			Code:     "rle_rollout_invalid_json_payload",
			Category: azdext.LocalErrorCategoryUser,
		}
	}
	return json.RawMessage(raw), nil
}

// repairWindowsPowerShellJSONObject restores a simple object whose JSON quotes Windows PowerShell
// removes when passing a single-quoted native-command argument. It intentionally supports only
// top-level scalar fields, which matches Gym reset payloads such as {seed: 32, split: train}.
func repairWindowsPowerShellJSONObject(raw []byte) ([]byte, bool) {
	input := strings.TrimSpace(string(raw))
	if len(input) < 3 || input[0] != '{' || input[len(input)-1] != '}' {
		return nil, false
	}

	fields := splitPowerShellObjectFields(input[1 : len(input)-1])
	repaired := make([]byte, 0, len(input)+8)
	repaired = append(repaired, '{')
	for index, field := range fields {
		key, value, found := strings.Cut(field, ":")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !found || !isPowerShellBareKey(key) || value == "" {
			return nil, false
		}

		if index > 0 {
			repaired = append(repaired, ',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, false
		}
		repaired = append(repaired, encodedKey...)
		repaired = append(repaired, ':')
		if isJSONScalar(value) {
			repaired = append(repaired, value...)
			continue
		}
		if strings.ContainsAny(value, "{}[]\"'") {
			return nil, false
		}
		encodedValue, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		repaired = append(repaired, encodedValue...)
	}
	repaired = append(repaired, '}')
	return repaired, true
}

// splitPowerShellObjectFields separates fields only at commas followed by a bare object key.
// It leaves commas within scalar string values unchanged.
func splitPowerShellObjectFields(input string) []string {
	fields := make([]string, 0, 1)
	fieldStart := 0
	for index := range input {
		if input[index] != ',' {
			continue
		}
		remainder := strings.TrimSpace(input[index+1:])
		key, _, found := strings.Cut(remainder, ":")
		if !found || !isPowerShellBareKey(strings.TrimSpace(key)) {
			continue
		}
		fields = append(fields, input[fieldStart:index])
		fieldStart = index + 1
	}
	return append(fields, input[fieldStart:])
}

func isJSONScalar(value string) bool {
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return false
	}
	switch parsed.(type) {
	case nil, bool, float64, string:
		return true
	default:
		return false
	}
}

func isPowerShellBareKey(value string) bool {
	for index, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			character == '_' ||
			(index > 0 && ((character >= '0' && character <= '9') || character == '-')) {
			continue
		}
		return false
	}
	return value != ""
}

func newRolloutID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate rollout id: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func printRolloutResult(out interface{ Write([]byte) (int, error) }, response *executeRolloutResponse) error {
	if _, err := fmt.Fprintf(out, "Rollout %s complete.\n", response.RolloutID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		out,
		"  reward:  %s\n",
		strconv.FormatFloat(response.Reward, 'g', -1, 64),
	); err != nil {
		return err
	}
	if response.Success != nil {
		if _, err := fmt.Fprintf(out, "  success: %t\n", *response.Success); err != nil {
			return err
		}
	}
	if response.Episode != nil {
		if _, err := fmt.Fprintf(
			out,
			"  episode: %s (%s), %d step(s)\n",
			response.Episode.Kind,
			response.Episode.TerminationReason,
			len(response.Episode.Steps),
		); err != nil {
			return err
		}
		if response.Episode.Ungraded != nil && *response.Episode.Ungraded {
			if _, err := fmt.Fprintln(out, "  ungraded: true"); err != nil {
				return err
			}
		}
	}
	if len(response.Result) > 0 {
		if _, err := fmt.Fprintf(out, "  result:  %s\n", string(response.Result)); err != nil {
			return err
		}
	}
	return nil
}
