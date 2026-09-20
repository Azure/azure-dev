// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// rolloutFlags holds the CLI-facing configuration for one rollout.
type rolloutFlags struct {
	version        string
	model          string
	loraRank       int
	task           string
	taskFile       string
	agentInput     string
	agentInputFile string
	rolloutID      string
	sequenceID     int
	timeout        int
}

type rolloutAction struct {
	cmd             *cobra.Command
	flags           *rolloutFlags
	environmentName string
}

// rolloutTarget identifies the exact published environment version to run, and the
// Foundry project that owns it.
type rolloutTarget struct {
	environmentName string
	projectEndpoint string
	version         string
}

func newRolloutCommand() *cobra.Command {
	flags := &rolloutFlags{
		loraRank: 16,
		timeout:  600,
	}

	cmd := &cobra.Command{
		Use:   "rollout [environment-name]",
		Short: "Execute one rollout of a published RLE environment",
		Long: `Execute one rollout of a published RLE environment.

rollout provisions everything a Loom-backed rollout needs and tears it down again: it
creates a real Loom training session for --model, saves a sampler checkpoint, calls RLE's
Execute Rollout API with your task (and, for Harness targets, agent input), prints the
resulting reward and trajectory summary, then closes the Loom session. You never handle
Loom session or checkpoint identifiers directly.

With no environment name, rollout uses rle.name and rle.version from the current folder's
rle.toml. To run an environment without local source, provide both its name and
--version, then set FOUNDRY_PROJECT_ENDPOINT.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			environmentName := ""
			if len(args) == 1 {
				environmentName = args[0]
			}
			return (&rolloutAction{
				cmd:             cmd,
				flags:           flags,
				environmentName: environmentName,
			}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.version, "version", "", "Published environment version to run this rollout against.")
	cmd.Flags().StringVar(
		&flags.model,
		"model",
		"",
		"Loom base model name to bind for this rollout, e.g. Qwen/Qwen3-32B. "+
			"Required unless rle.toml sets defaults.model.name.",
	)
	cmd.Flags().IntVar(&flags.loraRank, "lora-rank", flags.loraRank, "LoRA adapter rank for the Loom session.")
	cmd.Flags().StringVar(&flags.task, "task", "", "Inline JSON task payload for the sandbox reset operation.")
	cmd.Flags().StringVar(&flags.taskFile, "task-file", "", "Path to a JSON file with the task payload.")
	cmd.Flags().StringVar(
		&flags.agentInput,
		"agent-input",
		"",
		"Inline JSON agent input (Harness targets only).",
	)
	cmd.Flags().StringVar(
		&flags.agentInputFile,
		"agent-input-file",
		"",
		"Path to a JSON file with the agent input (Harness targets only).",
	)
	cmd.Flags().StringVar(
		&flags.rolloutID,
		"rollout-id",
		"",
		"Caller-generated rollout correlation id. Defaults to a generated GUID.",
	)
	cmd.Flags().IntVar(&flags.sequenceID, "sequence-id", 0, "Loom training-step sequence id for this rollout.")
	cmd.Flags().IntVar(&flags.timeout, "timeout", flags.timeout, "Loom session provisioning timeout in seconds.")
	return cmd
}

func (a *rolloutAction) Run() error {
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
			Message:  "--model is required to bind a rollout to a Loom training session.",
			Code:     "rle_rollout_model_required",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: "Pass --model with a Loom base model name (for example --model Qwen/Qwen3-32B), " +
				"or set defaults.model.name in rle.toml.",
		}
	}

	task, err := readJSONFlagOrFile("--task", a.flags.task, "--task-file", a.flags.taskFile)
	if err != nil {
		return err
	}
	agentInput, err := readJSONFlagOrFile("--agent-input", a.flags.agentInput, "--agent-input-file", a.flags.agentInputFile)
	if err != nil {
		return err
	}

	rolloutID := strings.TrimSpace(a.flags.rolloutID)
	if rolloutID == "" {
		rolloutID, err = newRolloutID()
		if err != nil {
			return err
		}
	}

	ctx, stopSignals := signal.NotifyContext(a.cmd.Context(), os.Interrupt)
	defer stopSignals()

	loom, err := createLoomSessionClient(target.projectEndpoint)
	if err != nil {
		return err
	}
	timeout := time.Duration(a.flags.timeout) * time.Second

	out := a.cmd.OutOrStdout()
	errOut := a.cmd.ErrOrStderr()

	if _, err := fmt.Fprintf(out, "Creating Loom training session for model %s ...\n", model); err != nil {
		return err
	}
	sessionID, err := loom.createSession(ctx, model, a.flags.loraRank, timeout)
	if err != nil {
		return loomServiceErrorFor("create Loom training session", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if cerr := loom.closeSession(cctx, sessionID); cerr != nil {
			_, _ = fmt.Fprintln(errOut, "Warning: failed to close Loom session; it may remain allocated.")
			return
		}
		_, _ = fmt.Fprintln(errOut, "Loom session closed.")
	}()

	checkpointName := fmt.Sprintf("azd-rollout-%s", rolloutID[:8])
	if _, err := fmt.Fprintln(out, "Saving Loom sampler checkpoint ..."); err != nil {
		return err
	}
	checkpointID, err := loom.saveWeightsForSampler(ctx, sessionID, a.flags.sequenceID, checkpointName, timeout)
	if err != nil {
		return loomServiceErrorFor("save Loom sampler checkpoint", err)
	}

	loomToken, err := loom.bearerToken(ctx)
	if err != nil {
		return fmt.Errorf("authenticate to Loom for Execute Rollout: %w", err)
	}

	sequenceID := int64(a.flags.sequenceID)
	if _, err := fmt.Fprintf(
		out,
		"Executing rollout %s for environment %s version %s ...\n",
		rolloutID,
		target.environmentName,
		target.version,
	); err != nil {
		return err
	}
	response, err := rle.executeRollout(ctx, target.environmentName, target.version, loomToken, executeRolloutRequest{
		RolloutID:  rolloutID,
		Task:       task,
		AgentInput: agentInput,
		Model: &rolloutModelSelection{
			ModelName:       model,
			LoomSessionID:   sessionID,
			CheckpointID:    checkpointID,
			SequenceID:      &sequenceID,
			ProjectEndpoint: target.projectEndpoint,
		},
	})
	if err != nil {
		if isRleNotFound(err) {
			return environmentVersionNotFoundError(target.environmentName, target.version)
		}
		return serviceError(err)
	}

	return printRolloutResult(out, response)
}

func (a *rolloutAction) resolveTarget() (rolloutTarget, *rleClient, error) {
	requestedVersion := strings.TrimSpace(a.flags.version)
	if a.cmd.Flags().Changed("version") && requestedVersion == "" {
		return rolloutTarget{}, nil, &azdext.LocalError{
			Message:    "--version requires a non-empty environment version.",
			Code:       "rle_environment_version_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Provide a semantic version, for example --version 2.1.0.",
		}
	}
	environmentName := strings.TrimSpace(a.environmentName)
	if environmentName == "" {
		config, err := project.LoadRleConfig(".")
		if err != nil {
			return rolloutTarget{}, nil, err
		}
		environmentName = config.Rle.Name
		if requestedVersion == "" {
			requestedVersion = config.Rle.Version
		}
	} else if requestedVersion == "" {
		return rolloutTarget{}, nil, &azdext.LocalError{
			Message:    "A published RLE version is required when invoking by environment name.",
			Code:       "rle_environment_version_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Pass --version <major.minor.patch>, or run from a folder with rle.toml.",
		}
	}
	version, err := project.NormalizeRleVersion(requestedVersion)
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
	}, client, nil
}

// defaultModelFromRleConfig best-effort loads rle.toml from the current folder and returns
// defaults.model.name, so --model can be omitted when the manifest already declares one.
// Any load error (including no rle.toml present) is treated as "no default available"
// rather than a hard failure, since --model is only required when no default exists.
func defaultModelFromRleConfig() string {
	config, err := project.LoadRleConfig(".")
	if err != nil || config.Defaults == nil || config.Defaults.Model == nil || config.Defaults.Model.Name == nil {
		return ""
	}
	return strings.TrimSpace(*config.Defaults.Model.Name)
}

// readJSONFlagOrFile reads a JSON payload from an inline flag or a file flag (at most one
// may be set) and validates it parses as JSON. Returns nil if neither is set.
func readJSONFlagOrFile(inlineName, inline, fileName, file string) (json.RawMessage, error) {
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

	var probe any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, &azdext.LocalError{
			Message:  fmt.Sprintf("%s must contain valid JSON: %v", firstNonEmpty(fileName, inlineName), err),
			Code:     "rle_rollout_invalid_json_payload",
			Category: azdext.LocalErrorCategoryUser,
		}
	}
	return json.RawMessage(raw), nil
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
		"  reward:  %s\n  success: %t\n",
		strconv.FormatFloat(response.Reward, 'g', -1, 64),
		response.Success,
	); err != nil {
		return err
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
	}
	if len(response.Result) > 0 {
		if _, err := fmt.Fprintf(out, "  result:  %s\n", string(response.Result)); err != nil {
			return err
		}
	}
	return nil
}
