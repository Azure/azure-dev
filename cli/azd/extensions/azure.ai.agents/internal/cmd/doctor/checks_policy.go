// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/foundrypolicy"
	"azureaiagent/internal/project"
)

const foundryGovernPortalFallback = "https://ai.azure.com/?flight=govern_ignite"

var (
	embeddedSubscriptionRegex    = regexp.MustCompile(`(?i)/subscriptions/[^\s"',)]+`)
	embeddedManagementGroupRegex = regexp.MustCompile(`(?i)/providers/Microsoft\.Management/managementGroups/[^\s"',)]+`)
	embeddedPortalURLRegex       = regexp.MustCompile(`(?i)https://ai\.azure\.com/[^\s"',)]+`)
)

func redactARMResourceID(raw string) string {
	trimmed := strings.TrimRight(raw, ".:;]")
	suffix := raw[len(trimmed):]
	return redactScope(trimmed, false) + suffix
}

func sanitizePolicyText(text string, unredacted bool) string {
	if unredacted || text == "" {
		return text
	}
	text = embeddedPortalURLRegex.ReplaceAllStringFunc(text, func(raw string) string {
		trimmed := strings.TrimRight(raw, ".:;]")
		suffix := raw[len(trimmed):]
		return redactedPlaceholder + suffix
	})
	text = embeddedSubscriptionRegex.ReplaceAllStringFunc(text, redactARMResourceID)
	text = embeddedManagementGroupRegex.ReplaceAllStringFunc(text, redactARMResourceID)
	return text
}

// newCheckPolicy produces Check `remote.policy`. It evaluates active Azure Policy
// assignments on the Foundry project scope against the configured azure.ai.agent
// services (agent kinds, container images/registries, models, and tools).
//
//   - Pass: all configured agent services comply with active Foundry Azure Policy assignments.
//   - Fail: one or more agent services are blocked by a Deny policy assignment; surfaces
//     the blocking policy display names, affected services, and Foundry Govern portal links.
//   - Skip: precondition unmet or policy inventory unavailable (best-effort fail-open).
func newCheckPolicy(deps Dependencies) Check {
	return Check{
		ID:     "remote.policy",
		Name:   "Agent configuration complies with Foundry Azure Policy",
		Remote: true,
		Fn: func(ctx context.Context, opts Options, prior []Result) Result {
			if deps.AzdClient == nil {
				return Result{
					Status:  StatusSkip,
					Message: "skipped: azd extension not reachable.",
				}
			}
			if priorBlocked(prior, "local.environment-selected") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: no azd environment is selected " +
						"(see check `local.environment-selected`).",
				}
			}
			if priorBlocked(prior, "remote.auth") {
				return Result{
					Status: StatusSkip,
					Message: "skipped: auth probe did not succeed " +
						"(see check `remote.auth`).",
				}
			}

			projectIDReader := deps.readProjectResourceIDFn
			if projectIDReader == nil {
				projectIDReader = readProjectResourceID
			}
			projectID, err := projectIDReader(ctx, deps.AzdClient)
			if err != nil {
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: could not read %s from the current azd environment (%s).",
						projectIDVar, err,
					),
				}
			}
			if strings.TrimSpace(projectID) == "" {
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: %s is not set in the current azd environment.",
						projectIDVar,
					),
				}
			}

			if err := project.ValidateProjectResourceID(projectID); err != nil {
				details := map[string]any{
					"projectId": redactScope(projectID, opts.Unredacted),
				}
				if opts.Unredacted {
					details["validateError"] = err.Error()
				}
				return Result{
					Status: StatusSkip,
					Message: fmt.Sprintf(
						"skipped: %s is not a valid Foundry project ARM resource ID.",
						projectIDVar,
					),
					Details: details,
				}
			}

			probe := deps.probeProjectPolicy
			if probe == nil {
				probe = project.QueryProjectPolicy
			}
			res, err := probe(ctx, deps.AzdClient, projectID)
			if err != nil {
				return classifyPolicyProbeError(err, projectID, opts.Unredacted)
			}
			return classifyPolicyResult(res, opts.Unredacted)
		},
	}
}

func classifyPolicyProbeError(err error, projectID string, unredacted bool) Result {
	if errors.Is(err, context.Canceled) {
		return Result{
			Status:  StatusSkip,
			Message: "skipped: Azure Policy probe was cancelled.",
		}
	}

	displayErr := firstLine(err.Error())
	if !unredacted {
		displayErr = sanitizeScopeARNs(displayErr)
	}
	details := map[string]any{
		"projectId": redactScope(projectID, unredacted),
	}
	if unredacted {
		details["probeError"] = err.Error()
	}
	return Result{
		Status: StatusSkip,
		Message: fmt.Sprintf(
			"skipped: could not evaluate Foundry Azure Policy (%s).",
			displayErr,
		),
		Suggestion: "Retry `azd ai agent doctor` after a moment; deployment will defer " +
			"policy enforcement to the Microsoft Foundry service.",
		Details: details,
	}
}

func classifyPolicyResult(res *project.ProjectPolicyResult, unredacted bool) Result {
	if res == nil || len(res.Services) == 0 {
		return Result{
			Status:  StatusSkip,
			Message: "skipped: no azure.ai.agent services found in azure.yaml.",
		}
	}

	scopeShort := fmt.Sprintf("%s/%s", res.AccountName, res.ProjectName)
	var violationSummaries []string
	var indeterminateSummaries []string
	var links []string
	var detailsServices []map[string]any
	totalViolations := 0

	for _, svc := range res.Services {
		var svcViolations []map[string]any
		for _, v := range svc.Violations {
			totalViolations++
			sanitizedMsg := sanitizePolicyText(strings.TrimSpace(v.Message), unredacted)
			sanitizedTargetVal := sanitizePolicyText(v.TargetValue, unredacted)
			summary := fmt.Sprintf("service %q: %s", svc.ServiceName, sanitizedMsg)
			if sanitizedMsg == "" {
				summary = fmt.Sprintf(
					"service %q: blocked by policy %q (%s: %s)",
					svc.ServiceName, v.DisplayName, v.Target, sanitizedTargetVal,
				)
			}
			if !slices.Contains(violationSummaries, summary) {
				violationSummaries = append(violationSummaries, summary)
			}

			link := foundryGovernPortalFallback
			if unredacted && strings.TrimSpace(v.PortalURL) != "" {
				link = strings.TrimSpace(v.PortalURL)
			}
			if !slices.Contains(links, link) {
				links = append(links, link)
			}

			svcViolations = append(svcViolations, map[string]any{
				"target":       string(v.Target),
				"targetValue":  sanitizedTargetVal,
				"displayName":  v.DisplayName,
				"assignmentId": redactScope(v.AssignmentID, unredacted),
				"message":      sanitizedMsg,
			})
		}

		svcEntry := map[string]any{
			"serviceName": svc.ServiceName,
			"agentKind":   svc.AgentKind,
			"violations":  svcViolations,
		}
		if svc.Outcome != "" {
			svcEntry["outcome"] = string(svc.Outcome)
		}
		if svc.Outcome == foundrypolicy.OutcomeIndeterminate {
			reason := sanitizePolicyText(strings.TrimSpace(svc.IncompleteReason), unredacted)
			if reason == "" {
				reason = "policy compliance could not be fully verified"
			}
			svcEntry["incompleteReason"] = reason
			indeterminateSummaries = append(
				indeterminateSummaries,
				fmt.Sprintf("service %q: %s", svc.ServiceName, reason),
			)
		}
		detailsServices = append(detailsServices, svcEntry)
	}

	details := map[string]any{
		"accountName":  res.AccountName,
		"projectName":  res.ProjectName,
		"projectScope": redactScope(res.ProjectScope, unredacted),
		"services":     detailsServices,
	}

	if totalViolations == 0 {
		if len(indeterminateSummaries) > 0 {
			return Result{
				Status: StatusSkip,
				Message: fmt.Sprintf(
					"skipped: Azure Policy compliance could not be fully verified for project '%s' (%s).",
					scopeShort, strings.Join(indeterminateSummaries, "; "),
				),
				Suggestion: "Re-run `azd ai agent doctor` after a moment, or proceed with deployment to let " +
					"the Microsoft Foundry service enforce policy compliance.",
				Details: details,
			}
		}
		return Result{
			Status: StatusPass,
			Message: fmt.Sprintf(
				"All configured agent services (%d) comply with active Foundry Azure Policy "+
					"assignments on project '%s'.",
				len(res.Services), scopeShort,
			),
			Details: details,
		}
	}

	suggestion := "Update the agent configuration in azure.yaml (agent kind, container image/registry, " +
		"model, or tools) to comply with your organization's Azure Policy assignments, or select a " +
		"different Foundry project."
	if !unredacted {
		suggestion += " Re-run with `--unredacted` to include full resource IDs and policy assignment links."
	}

	return Result{
		Status: StatusFail,
		Message: fmt.Sprintf(
			"Azure Policy on project '%s' blocks deployment: %s",
			scopeShort, strings.Join(violationSummaries, "; "),
		),
		Suggestion: suggestion,
		Links:      links,
		Details:    details,
	}
}
