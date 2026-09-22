// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"azureaiagent/internal/project"
)

const storageRBACLearnLink = "https://learn.microsoft.com/azure/foundry/how-to/bring-your-own-azure-storage-foundry"

func newCheckProjectStorageRBAC(deps Dependencies) Check {
	return Check{
		ID: "remote.project-storage-rbac", Name: "Project storage permissions", Remote: true,
		Fn: func(ctx context.Context, opts Options, prior []Result) Result {
			if opts.LocalOnly || errors.Is(ctx.Err(), context.Canceled) {
				return Result{Status: StatusSkip, Message: "skipped: remote check excluded or cancelled."}
			}
			if deps.AzdClient == nil || priorBlocked(prior, "local.environment-selected") ||
				priorBlocked(prior, "remote.auth") {
				return Result{
					Status: StatusSkip, Message: "skipped: azd connection, environment, or authentication unavailable.",
				}
			}
			readProjectID := deps.readProjectResourceIDFn
			if readProjectID == nil {
				readProjectID = readProjectResourceID
			}
			projectID, err := readProjectID(ctx, deps.AzdClient)
			if err != nil {
				return storageRBACQueryError(err)
			}
			if strings.TrimSpace(projectID) == "" {
				return Result{Status: StatusSkip, Message: "skipped: the Foundry project resource ID is not configured."}
			}
			probe := deps.probeProjectStorageRBAC
			if probe == nil {
				probe = project.QueryProjectStorageRBAC
			}
			result, err := probe(ctx, deps.AzdClient, projectID)
			if err != nil {
				return storageRBACQueryError(err)
			}
			return classifyProjectStorageRBAC(result, opts.Unredacted)
		},
	}
}

func storageRBACQueryError(err error) Result {
	if errors.Is(err, context.Canceled) {
		return Result{Status: StatusSkip, Message: "skipped: storage permission check was cancelled."}
	}
	if errors.Is(err, project.ErrInvalidProjectResourceID) {
		return Result{
			Status: StatusFail, Message: "The configured Foundry project resource ID is invalid.",
			Suggestion: "Correct AZURE_AI_PROJECT_ID in the current azd environment.",
		}
	}
	return Result{
		Status: StatusWarn, Message: "Could not verify project storage permissions.",
		Suggestion: "Verify access to project metadata, shared connections, and role assignments, then retry.",
		Links:      []string{storageRBACLearnLink},
	}
}

func classifyProjectStorageRBAC(result *project.ProjectStorageRBACResult, unredacted bool) Result {
	if result == nil {
		return storageRBACQueryError(errors.New("empty storage query result"))
	}
	if len(result.Findings) == 0 {
		return Result{Status: StatusSkip, Message: "skipped: no applicable customer-owned Storage connections were found."}
	}
	principal := redactID(result.PrincipalID, unredacted)
	var lines []string
	var findings []map[string]any
	var passed, failed, warned, skipped int
	missingRole := false
	for _, finding := range result.Findings {
		connectionName := redactDisplay(finding.ConnectionName, unredacted)
		scope := redactScope(finding.StorageScope, unredacted)
		switch finding.Status {
		case "granted":
			passed++
		case "missing":
			failed++
			missingRole = true
		case "invalid":
			failed++
		case "skip":
			skipped++
		default:
			warned++
		}
		lines = append(lines, fmt.Sprintf("Connection %q, Storage %q: %s.", connectionName, scope, finding.Message))
		findings = append(findings, map[string]any{
			"connection": connectionName, "storageScope": scope, "status": finding.Status, "message": finding.Message,
		})
	}
	response := Result{
		Status: StatusPass,
		Message: fmt.Sprintf("Project identity %s: %d storage checks passed, %d failed, %d unverified, %d skipped.\n%s",
			principal, passed, failed, warned, skipped, strings.Join(lines, "\n")),
		Links:   []string{storageRBACLearnLink},
		Details: map[string]any{"principalId": principal, "findings": findings},
	}
	if len(result.Findings) == 1 {
		finding := result.Findings[0]
		response.Message = fmt.Sprintf("Project identity %s, Storage %q: %s.",
			principal, redactScope(finding.StorageScope, unredacted), finding.Message)
	}
	switch {
	case failed > 0:
		response.Status = StatusFail
		response.Suggestion = "Correct the project identity or Storage connection configuration in the detailed report."
		if missingRole {
			response.Suggestion = fmt.Sprintf(
				"Ask an administrator with permission to assign roles on the affected Storage accounts "+
					"to grant Storage Blob Data Contributor to project identity %s.", principal)
			if len(result.Findings) == 1 {
				response.Suggestion = fmt.Sprintf(
					"Ask an administrator with permission to assign roles on %q "+
						"to grant Storage Blob Data Contributor to project identity %s.",
					redactScope(result.Findings[0].StorageScope, unredacted), principal)
			}
		}
	case warned > 0:
		response.Status = StatusWarn
		response.Suggestion = "Verify the Storage connection authentication and role-assignment read access; " +
			"have an administrator review any unresolved custom, conditional, or group grants."
	case passed == 0:
		response.Status = StatusSkip
		response.Message = "skipped: the configured Storage connections do not use the project managed identity."
	}
	if response.Status == StatusFail || response.Status == StatusWarn {
		response.Suggestion += " Use --debug for details; add --unredacted to show identifiers when sharing is safe."
	}
	return response
}
