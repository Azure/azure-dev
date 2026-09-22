// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
	"azureaieval/internal/urlsafe"
)

type generationResult struct {
	*project.ArtifactRef
	Status        string                `json:"status"`
	JobID         string                `json:"job_id,omitempty"`
	Error         string                `json:"error,omitempty"`
	Recovery      string                `json:"recovery_command,omitempty"`
	RetryGuidance string                `json:"retry_guidance,omitempty"`
	Guidance      string                `json:"guidance,omitempty"`
	Warnings      []eval_api.JobWarning `json:"warnings,omitempty"`
	Artifact      *project.ArtifactRef  `json:"artifact,omitempty"`
}

// Recovery reads the existing job before considering another billed submission.
// Even a failed submit can have reached the service without its reply reaching us.
func generationRecoveryCommand(
	outcome generationOutcome, flags generateFlags, endpoint, envName string,
) (string, error) {
	command := "azd ai eval job "
	if outcome.report.jobID == "" {
		command += "list --" + string(outcome.plan.Kind)
	} else {
		command += "show " + messages.ShellArg(outcome.report.jobID) + " --" + string(outcome.plan.Kind)
		if flags.path != "" {
			command += " --path " + messages.ShellArg(flags.path)
		}
		if outcome.plan.OutputDir != "" {
			command += " --output-dir " + messages.ShellArg(outcome.plan.OutputDir)
		}
	}
	return commandInProject(command, endpoint, envName)
}

func commandInProject(command, endpoint, envName string) (string, error) {
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil {
			return "", fmt.Errorf("preparing recovery command: %w", urlsafe.Error(err))
		}
		command += " --project-endpoint " + messages.ShellArg(urlsafe.URL(u))
	}
	if envName != "" {
		command += " --environment " + messages.ShellArg(envName)
	}
	return command, nil
}

func writeGenerationPartial(out io.Writer, outcomes []generationOutcome) {
	fmt.Fprint(out, messages.GenerationIncomplete())
	for _, o := range outcomes {
		kind := string(o.plan.Kind)
		switch {
		case o.ref != nil && o.err == nil:
			fmt.Fprint(out, messages.GenerationRetained(kind, o.ref.Name, o.ref.Version))
		case o.ref != nil:
			fmt.Fprint(out, messages.GenerationCatalogFailed(kind, o.ref.Name, o.ref.Version))
		case o.err == nil:
			fmt.Fprint(out, messages.GenerationJobLine(kind, o.report.jobID))
		default:
			fmt.Fprint(out, messages.GenerationDidNotFinish(kind, o.err))
		}
		if o.err != nil {
			fmt.Fprint(out, messages.GenerationRecovery(o.recovery,
				messages.GenerationRetryGuidance(kind, o.report.jobID != "")))
		}
	}
}

func generationCatalogGuidance(path string, kind generateKind, name string) string {
	cfg, err := project.OpenEvalConfig(path)
	if err != nil {
		return fmt.Sprintf("Artifact %q was declared, but its eval references could not be checked: %v", name, err)
	}
	if cfg == nil || len(cfg.Evals) == 0 {
		return ""
	}
	for _, eval := range cfg.Evals {
		if kind == generateKindDataset && eval.Dataset == name {
			return ""
		}
		if kind == generateKindEvaluator {
			for _, ref := range eval.Evaluators {
				if ref.Evaluator == name {
					return ""
				}
			}
		}
	}
	guidance := messages.GenerationDeclarationOnly(string(kind), name)
	if kind == generateKindDataset {
		for _, eval := range cfg.Evals {
			if eval.Source != nil && strings.EqualFold(eval.Source.Type, project.SourceTypeTraces) {
				return guidance + " " + messages.GeneratedDatasetNotForTraces()
			}
		}
	}
	return guidance
}
