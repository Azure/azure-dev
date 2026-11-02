// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/defaults"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newDefaultsCommand(extCtx *azdext.ExtensionContext, open sessionFactory) *cobra.Command {
	group := &cobra.Command{
		Use: "defaults", Short: "Read or update CU resource model deployment defaults, not local azd configuration",
	}
	for _, action := range []string{"show", "set"} {
		options := &config.Options{}
		var models []string
		var fromEnvironment, replace bool
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "show" {
			cmd.Short = "Show the complete Content Understanding service defaults"
			cmd.Example = `  azd ai cu defaults show --output json
    Show the resource's current model deployment mappings as JSON.`
		} else {
			cmd.Short = "Update Content Understanding service model deployment mappings"
			cmd.Flags().StringArrayVar(&models, "model", nil, "Model mapping MODEL=DEPLOYMENT (repeatable)")
			cmd.Flags().BoolVar(&fromEnvironment, "from-environment", false,
				"Include resolved cu.modelDeployments; explicit --model mappings take precedence")
			cmd.Flags().BoolVar(&replace, "replace", false,
				"Skip the client-side merge with existing defaults; service merge-patch semantics still apply")
			cmd.Example = `  azd ai cu defaults set --model gpt-5.2=gpt52 --model text-embedding-3-large=embedding3
    Add or update model deployment mappings on the resource.

  azd ai cu defaults set --from-environment --environment dev
    Apply the selected azd environment's cu.modelDeployments mappings.`
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			return exterrors.FromError(runDefaults(cmd, extCtx, open, options, action, models, fromEnvironment, replace))
		}
		group.AddCommand(serviceCommand(cmd, options))
	}
	return group
}

func runDefaults(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, open sessionFactory, options *config.Options,
	action string, models []string, fromEnvironment, replace bool,
) error {
	ctx := cmd.Context()
	if err := ctx.Err(); err != nil {
		return err
	}
	desired, err := defaults.ParseModels(models)
	if err != nil {
		return err
	}
	if action == "set" && len(desired) == 0 && !fromEnvironment {
		return defaults.MissingModels()
	}
	session, err := open(cmd, extCtx, *options)
	if err != nil {
		return err
	}
	defer session.close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if action == "set" {
		if fromEnvironment {
			desired = defaults.Merge(session.models, desired)
		}
		result, err := defaults.Apply(ctx, session.client, desired, replace)
		if err != nil {
			return err
		}
		raw, err := defaults.JSON(result)
		if err != nil {
			return err
		}
		return writeServiceResult(cmd, extCtx.OutputFormat, raw)
	}
	result, err := session.client.GetDefaults(ctx)
	if defaults.IsNotSet(err) {
		return commandError("defaults_not_set", "CU service defaults have not been configured.",
			"Run defaults set --model MODEL=DEPLOYMENT, or select an azd environment and use --from-environment.")
	}
	if err != nil {
		return err
	}
	raw, err := defaults.JSON(result)
	if err != nil {
		return err
	}
	return writeServiceResult(cmd, extCtx.OutputFormat, raw)
}
