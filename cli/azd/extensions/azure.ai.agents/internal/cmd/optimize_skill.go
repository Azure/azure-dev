// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

var (
	skillReferenceNamePattern    = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	skillReferenceVersionPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

func (c *OptimizeConfig) isSkillOptimization() bool {
	return c.Request != nil && c.Request.OptimizationConfiguration.Type == optimize_api.SkillOptimizationType
}

func validateSkillOptimizeRequest(request *optimize_api.OptimizeRequest) error {
	config := request.OptimizationConfiguration
	if request.TargetConfiguration == nil && config.EvaluationConfiguration != nil {
		return fmt.Errorf("evaluation_configuration requires a target agent for skill_optimization")
	}
	if config.Goal != "" || config.CandidateSearchConfiguration != nil ||
		config.BaselineAgentConfiguration != nil || config.AgentOptimizationSpace != nil {
		return fmt.Errorf("skill_optimization does not support goal, candidate_search_configuration, " +
			"baseline_agent_configuration, or agent_optimization_space")
	}
	if target := request.TargetConfiguration; target != nil {
		if target.Type != "foundry_agent" || !validSkillIdentity(target.Name, 256) ||
			(target.Version != "" && !validSkillVersion(target.Version)) {
			return fmt.Errorf("target_configuration must name a foundry_agent with an optional exact version")
		}
	}
	if evaluation := config.EvaluationConfiguration; evaluation != nil {
		if evaluation.TrainingSet != nil {
			return fmt.Errorf("skill evaluation_configuration does not accept training_set")
		}
		if !validSkillIdentity(evaluation.EvaluationModel.Model, 256) {
			return fmt.Errorf("skill evaluation_configuration requires evaluation_model.model")
		}
		generation := evaluation.EvaluatorGenerationConfiguration
		if generation != nil && generation.Type != "agent_traces" {
			return fmt.Errorf("evaluator_generation_configuration.type must be agent_traces")
		}
		if len(evaluation.Evaluators) == 0 && generation == nil {
			return fmt.Errorf("skill evaluation_configuration requires evaluators or evaluator_generation_configuration")
		}
		if evaluation.ValidationSet != nil && evaluation.ValidationSet.Type != "target_completion" {
			return fmt.Errorf("skill evaluation_configuration.validation_set.type must be target_completion")
		}
		if evaluation.MaxConcurrentAgentRuns != nil && *evaluation.MaxConcurrentAgentRuns < 1 {
			return fmt.Errorf("skill evaluation_configuration.max_concurrent_agent_runs must be positive")
		}
	}
	if len(config.AdditionalSkills) > 20 {
		return fmt.Errorf("additional_skills accepts at most 20 references")
	}
	traceContexts, steeringContexts := 0, 0
	references := append([]optimize_api.FoundrySkillReference{}, config.AdditionalSkills...)
	for _, context := range config.Context {
		switch context.Type {
		case "agent_traces":
			traceContexts++
			if context.Prompt != nil || context.SkillReferences != nil || context.TraceWindow == nil {
				return fmt.Errorf("agent_traces requires trace_window and cannot contain prompt or skill_references")
			}
			if err := validateSkillTraceWindow(context.TraceWindow); err != nil {
				return err
			}
			if err := validateSkillTraceConnection(context.TraceConnectionConfiguration, request.TargetConfiguration); err != nil {
				return err
			}
		case "steering_prompt":
			steeringContexts++
			if context.Prompt == nil || strings.TrimSpace(*context.Prompt) == "" ||
				context.TraceWindow != nil || context.TraceConnectionConfiguration != nil || context.SkillReferences != nil {
				return fmt.Errorf("steering_prompt requires a non-empty prompt and cannot contain trace or skill fields")
			}
			if len(*context.Prompt) > 100*1024 {
				return fmt.Errorf("steering_prompt.prompt must be at most 100 KiB (102400 UTF-8 bytes)")
			}
		case "skill_references":
			if context.SkillReferences == nil || context.Prompt != nil ||
				context.TraceWindow != nil || context.TraceConnectionConfiguration != nil {
				return fmt.Errorf("skill_references requires an array and cannot contain prompt or trace fields")
			}
			if len(context.SkillReferences) > 20 {
				return fmt.Errorf("skill_references accepts at most 20 references per list")
			}
			references = append(references, context.SkillReferences...)
		default:
			return fmt.Errorf("unsupported skill optimization context type %q", context.Type)
		}
	}
	if traceContexts != 1 || steeringContexts > 1 {
		return fmt.Errorf("skill_optimization requires exactly one agent_traces context and at most one steering_prompt")
	}
	versions := make(map[string]string)
	for _, reference := range references {
		if reference.Type != "foundry_skill" || len(reference.Name) > 64 ||
			!skillReferenceNamePattern.MatchString(reference.Name) || !validSkillVersion(reference.Version) {
			return fmt.Errorf("skill references require type foundry_skill, a lowercase hyphenated name " +
				"(maximum 64 characters), and an exact version (not latest or default)")
		}
		if version, exists := versions[reference.Name]; exists && version != reference.Version {
			return fmt.Errorf("conflicting versions for skill %q", reference.Name)
		}
		versions[reference.Name] = reference.Version
	}
	if len(versions) > 20 {
		return fmt.Errorf("skill_optimization accepts at most 20 distinct explicit skill references")
	}
	return nil
}

func validSkillIdentity(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.RuneCountInString(value) <= maximum &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validSkillVersion(value string) bool {
	return len(value) <= 128 && skillReferenceVersionPattern.MatchString(value) &&
		!strings.EqualFold(value, "latest") && !strings.EqualFold(value, "default")
}

func validateSkillTraceConnection(
	connection *optimize_api.SkillTraceConnectionConfiguration,
	target *optimize_api.AgentIdentifier,
) error {
	if connection == nil {
		if target == nil {
			return fmt.Errorf("without target_configuration, agent_traces requires " +
				"trace_connection_configuration with resource_id and agent_name")
		}
		return nil
	}
	resource, err := arm.ParseResourceID(connection.ResourceID)
	if connection.Type != "appinsights" || err != nil || resource.SubscriptionID == "" || resource.ResourceGroupName == "" ||
		!strings.EqualFold(resource.ResourceType.String(), "Microsoft.Insights/components") {
		return fmt.Errorf("trace_connection_configuration requires type appinsights and an App Insights component ARM resource_id")
	}
	if target != nil && connection.AgentName != nil {
		return fmt.Errorf("trace_connection_configuration.agent_name cannot be combined with target_configuration")
	}
	if target == nil && (connection.AgentName == nil || !validSkillIdentity(*connection.AgentName, 256) ||
		strings.ContainsAny(*connection.AgentName, "*?|;")) {
		return fmt.Errorf("agentless trace_connection_configuration requires an exact non-empty agent_name " +
			"without wildcards or KQL (maximum 256 characters)")
	}
	return nil
}

func validateSkillTraceTimestampNumbers(values map[string]any) error {
	config, _ := values["optimization_configuration"].(map[string]any)
	if config["type"] != optimize_api.SkillOptimizationType {
		return nil
	}
	contexts, _ := config["context"].([]any)
	for _, item := range contexts {
		context, _ := item.(map[string]any)
		if context["type"] != "agent_traces" {
			continue
		}
		window, _ := context["trace_window"].(map[string]any)
		for _, field := range []string{"start_time", "end_time"} {
			// Generic YAML decoding would otherwise turn 1.0 into integer JSON before validation.
			if _, fractional := window[field].(float64); fractional {
				return fmt.Errorf(
					"trace_window.%s timestamp requires integer Unix seconds, not floating-point values", field)
			}
		}
	}
	return nil
}

func validateSkillTraceWindow(window *optimize_api.SkillTraceWindow) error {
	switch window.Type {
	case "fixed":
		if window.TraceIDs != nil || window.LookbackHours != nil {
			return fmt.Errorf("a fixed trace_window cannot contain trace_ids or lookback_hours")
		}
		start, startErr := window.StartTime.Time()
		end, endErr := window.EndTime.Time()
		if startErr != nil || endErr != nil || start.Before(time.Unix(0, 0)) || !end.After(start) || end.After(time.Now()) {
			return fmt.Errorf("fixed trace_window requires integer Unix seconds or timezone-aware ISO " +
				"start_time and end_time " +
				"with end_time after start_time and not in the future, and start_time on or after 1970-01-01")
		}
		if window.MaxTraces != nil && (*window.MaxTraces < 1 || *window.MaxTraces > 100) {
			return fmt.Errorf("trace_window.max_traces must be between 1 and 100")
		}
	case "trace_ids":
		if len(window.StartTime) != 0 || len(window.EndTime) != 0 || window.MaxTraces != nil {
			return fmt.Errorf("a trace_ids window cannot contain start_time, end_time, or max_traces")
		}
		if len(window.TraceIDs) < 1 || len(window.TraceIDs) > 100 ||
			window.LookbackHours == nil || *window.LookbackHours < 1 || *window.LookbackHours > 2147483647 {
			return fmt.Errorf("trace_ids window requires 1 to 100 unique trace_ids and lookback_hours between 1 and 2147483647")
		}
		seen := make(map[string]bool)
		for _, id := range window.TraceIDs {
			if !validSkillIdentity(id, 256) || seen[id] {
				return fmt.Errorf("trace_ids must be unique non-empty IDs without surrounding whitespace or control characters")
			}
			seen[id] = true
		}
	default:
		return fmt.Errorf("trace_window.type must be fixed or trace_ids")
	}
	return nil
}

func printSkillOptimizeCandidate(out io.Writer, jobID string, candidate optimize_api.CandidateResult) {
	fmt.Fprintf(out, "\n  Candidate: %s (%s)\n  Status: %s\n", candidate.Name, candidate.CandidateID, candidate.Status)
	if candidate.Error != nil {
		fmt.Fprintf(out, "  Error [%s]: %s\n", candidate.Error.Code, candidate.Error.Message)
	}
	if candidate.Rationale != "" {
		fmt.Fprintf(out, "  Rationale: %s\n", candidate.Rationale)
	}
	if candidate.ExecutionSummary != nil {
		fmt.Fprintln(out, "  Execution:")
		for _, step := range candidate.ExecutionSummary.Steps {
			fmt.Fprintf(out, "    %s: %s", step.Name, step.Status)
			if step.Summary != "" {
				fmt.Fprintf(out, " - %s", step.Summary)
			}
			if step.ErrorCode != "" {
				fmt.Fprintf(out, " [%s]", step.ErrorCode)
			}
			fmt.Fprintln(out)
		}
	}
	if candidate.Output == nil || candidate.Output.SkillResults == nil {
		fmt.Fprintln(out, "  Skill outcomes have not been reported.")
	} else if len(candidate.Output.SkillResults) == 0 {
		fmt.Fprintln(out, "  No skill changes were reported.")
	} else {
		for _, result := range candidate.Output.SkillResults {
			fmt.Fprintf(out, "\n    Skill: %s\n    Change: %s\n    Outcome: %s\n",
				result.OutputSkill.Name, result.ChangeType, result.Status)
			if result.Status == "created" && result.OutputSkill.Version != nil {
				fmt.Fprintf(out, "    Published version: %s\n", *result.OutputSkill.Version)
			}
			if result.InputSkill != nil {
				fmt.Fprintf(out, "    Baseline version: %s\n", result.InputSkill.Version)
			}
			if result.Rationale != "" {
				fmt.Fprintf(out, "    Rationale: %s\n", result.Rationale)
			}
			if result.Error != nil {
				fmt.Fprintf(out, "    Error [%s]: %s\n", result.Error.Code, result.Error.Message)
			}
			if result.Status == "publication_unknown" {
				fmt.Fprintln(out, "    Warning: publication is unconfirmed; verify the skill before retrying.")
			}
			for _, validation := range result.Validations {
				if validation.Status != "passed" {
					fmt.Fprintf(out, "    Validation %s/%s: %s - %s\n",
						validation.Type, validation.ID, validation.Status, validation.Details)
				}
			}
		}
	}
	for _, file := range candidate.Files {
		fmt.Fprintf(out, "\n  File: %s (%s, %d bytes)\n", file.Path, file.Type, file.SizeInBytes)
		fmt.Fprintf(out, "    azd ai agent optimize download %q --candidate %q --file %q\n",
			jobID, candidate.CandidateID, file.Path)
	}
	fmt.Fprintln(out, "\n  Skill versions are not automatically attached or deployed to an agent.")
}
