// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/defaults"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type doctorResult struct {
	Ready          bool              `json:"ready"`
	Endpoint       string            `json:"endpoint"`
	APIVersion     string            `json:"apiVersion"`
	Authentication string            `json:"authentication"`
	Profile        string            `json:"profile"`
	Analyzer       *string           `json:"defaultAnalyzer"`
	Models         map[string]string `json:"modelDeployments"`
	Missing        []string          `json:"missingRequirements"`
	Failures       []doctorFailure   `json:"failures"`
	Sources        map[string]string `json:"sources"`
}

type doctorFailure struct {
	Check      string `json:"check"`
	Message    string `json:"message"`
	StatusCode int    `json:"statusCode,omitempty"`
	Code       string `json:"code,omitempty"`
	RequestID  string `json:"requestId,omitempty"`
}

func newDoctorCommand(extCtx *azdext.ExtensionContext, open sessionFactory) *cobra.Command {
	options := &config.Options{}
	var fix bool
	cmd := &cobra.Command{
		Use: "doctor", Args: cobra.NoArgs,
		Short: "Check azd context, Entra authentication, CU access, and generative readiness",
		Long: "Resolve azd environment or projectless configuration, authenticate through azd, " +
			"and read CU service defaults. Show effective CU settings and their configuration sources.\n" +
			"Missing generative mappings return ready:false; " +
			"configuration/auth/service failures use the host error channel.\n" +
			"No resources or deployments are created. Only --fix-defaults permits a service-defaults update.",
		Example: `  azd ai cu doctor
    Check authentication, resource access, and model readiness.

  azd ai cu doctor --endpoint https://resource.services.ai.azure.com/
    Check a resource directly without requiring an azd project.

  azd ai cu doctor --fix-defaults --environment dev
    Configure service defaults from the selected environment's model mappings.`,
	}
	cmd.Flags().BoolVar(&fix, "fix-defaults", false,
		"Merge resolved cu.modelDeployments into service defaults and derive prebuilt aliases")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return exterrors.FromError(runDoctor(cmd, extCtx, open, *options, fix))
	}
	return serviceCommand(cmd, options)
}

func runDoctor(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, open sessionFactory, options config.Options, fix bool,
) error {
	ctx := cmd.Context()
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := open(cmd, extCtx, options)
	if err != nil {
		return doctorConfigurationError(err)
	}
	defer session.close()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := session.client.GetDefaults(ctx)
	if err != nil && !defaults.IsNotSet(err) {
		return err
	}
	mapping := map[string]string{}
	if err == nil {
		if _, err := defaults.JSON(current); err != nil {
			return err
		}
		mapping = current.ModelDeployments
	}
	if fix {
		mapping = defaults.Merge(mapping, session.models)
		updated, err := defaults.Apply(ctx, session.client, mapping, true)
		if err != nil {
			return err
		}
		mapping = updated.ModelDeployments
	}
	environment := session.environment
	if environment == "" {
		environment = "projectless"
	}
	var analyzer *string
	if session.analyzer != "" {
		analyzer = new(session.analyzer)
	}
	missing := defaults.MissingRequirements(mapping)
	return writeServiceResult(cmd, extCtx.OutputFormat, doctorResult{
		Ready: len(missing) == 0, Endpoint: session.endpoint, APIVersion: session.apiVersion,
		Authentication: "Microsoft Entra ID", Profile: environment, Analyzer: analyzer,
		Models: mapping, Missing: missing, Failures: []doctorFailure{}, Sources: session.sources,
	})
}

func doctorConfigurationError(err error) error {
	safe := exterrors.FromError(err)
	if local, ok := errors.AsType[*azdext.LocalError](safe); ok {
		result := *local
		switch result.Code {
		case "missing_endpoint", "invalid_endpoint":
			result.Suggestion = "Pass --endpoint, set process CU_ENDPOINT, use azd env set CU_ENDPOINT, " +
				"or save a projectless default with " +
				"azd ai cu config set CU_ENDPOINT https://<resource>.services.ai.azure.com."
		case exterrors.CodeMissingTenant:
			result.Suggestion = "Set AZURE_TENANT_ID or AZURE_SUBSCRIPTION_ID in the selected environment using " +
				"azd env set. For projectless use, pass an endpoint without selecting an environment. " +
				"Authenticate using azd auth login."
		case exterrors.CodeEnvironmentUnavailable:
			result.Suggestion = "Use azd env list/select to choose an existing project environment. " +
				"For projectless diagnostics, supply --endpoint and process AZURE_TENANT_ID without --environment."
		case "unsupported_api_version":
			result.Suggestion = "Pass --api-version 2025-11-01 or a valid YYYY-MM-DD-preview date, " +
				"or correct CU_API_VERSION using azd env set or azd ai cu config set."
		}
		return &result
	}
	return safe
}
