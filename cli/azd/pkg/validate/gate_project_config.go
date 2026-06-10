// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validate

import (
	"context"
	"fmt"
)

// NewProjectConfigGate creates a gate that validates the project configuration
// from azure.yaml. This is a built-in gate that checks for common configuration
// issues before provisioning or deployment.
func NewProjectConfigGate() Gate {
	gate := &CheckBasedGate{GateName: "project-config"}

	gate.AddCheck(Check{
		RuleID: "project_has_services",
		Fn:     checkProjectHasServices,
	})

	gate.AddCheck(Check{
		RuleID: "services_have_hosts",
		Fn:     checkServicesHaveHosts,
	})

	return gate
}

// checkProjectHasServices verifies that the project defines at least one service.
func checkProjectHasServices(
	ctx context.Context, pCtx *PipelineContext,
) ([]CheckResult, error) {
	if pCtx.Project == nil {
		return []CheckResult{{
			Severity:     CheckError,
			DiagnosticID: "project_not_loaded",
			Message:      "Project configuration (azure.yaml) could not be loaded.",
			Suggestion:   "Ensure you are running from a directory with an azure.yaml file.",
		}}, nil
	}

	if len(pCtx.Project.Services) == 0 {
		return []CheckResult{{
			Severity:     CheckWarning,
			DiagnosticID: "no_services_defined",
			Message:      "No services are defined in azure.yaml.",
			Suggestion: "Add a service definition to azure.yaml. " +
				"See https://aka.ms/azure-dev/azure.yaml.schema",
		}}, nil
	}

	return nil, nil
}

// checkServicesHaveHosts verifies that each service has a host configured.
func checkServicesHaveHosts(
	ctx context.Context, pCtx *PipelineContext,
) ([]CheckResult, error) {
	if pCtx.Project == nil {
		return nil, nil
	}

	var results []CheckResult
	for name, svc := range pCtx.Project.Services {
		if svc.Host == "" {
			results = append(results, CheckResult{
				Severity:     CheckWarning,
				DiagnosticID: "service_missing_host",
				Message: fmt.Sprintf(
					"Service %q does not have a host configured.",
					name,
				),
				Suggestion: fmt.Sprintf(
					"Add a 'host' field to the %q service in azure.yaml "+
						"(e.g. host: containerapp).",
					name,
				),
			})
		}
	}

	return results, nil
}
