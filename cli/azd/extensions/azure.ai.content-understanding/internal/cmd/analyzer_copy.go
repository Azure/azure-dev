// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"azure.ai.content-understanding/internal/analyzercopy"
	"azure.ai.content-understanding/internal/armclient"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type copySide struct {
	id, resource, subscription, group string
}

func newAnalyzerCopyCommand(extCtx *azdext.ExtensionContext, open sessionFactory) *cobra.Command {
	var options config.Options
	var source, destination copySide
	cmd := &cobra.Command{
		Use: "copy [SOURCE] [DESTINATION]", Short: "Copy an analyzer without replacing an existing destination",
		Example: `  azd ai cu analyzer copy invoice_v1 invoice_v2
    Copy an analyzer to a new ID in the connected resource.

  azd ai cu analyzer copy invoice_v1 invoice_v1 \
    --source-resource https://source.services.ai.azure.com/ --source-subscription source-sub \
    --destination-resource https://target.services.ai.azure.com/ --destination-subscription target-sub
    Copy between resources with explicit subscriptions for ARM discovery.`,
		Args: cobra.MaximumNArgs(2),
	}
	for _, side := range []struct {
		name    string
		options *copySide
	}{{"source", &source}, {"destination", &destination}} {
		flags := cmd.Flags()
		flags.StringVar(&side.options.id, side.name, "", "Analyzer ID at the "+side.name+" resource")
		flags.StringVar(&side.options.resource, side.name+"-resource", "", "Account name, ARM account ID, or HTTPS endpoint")
		flags.StringVar(&side.options.subscription, side.name+"-subscription", "", "Subscription for account discovery")
		flags.StringVar(&side.options.group, side.name+"-resource-group", "", "Resource group for account discovery")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return exterrors.FromError(runAnalyzerCopy(cmd, extCtx, open, options, source, destination, args))
	}
	return serviceCommand(cmd, &options)
}

func runAnalyzerCopy(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, open sessionFactory,
	options config.Options, source, destination copySide, args []string,
) error {
	sides := []*copySide{&source, &destination}
	for i, name := range []string{"source", "destination"} {
		side := sides[i]
		for flag, value := range map[string]string{
			name + "-resource":       side.resource,
			name + "-subscription":   side.subscription,
			name + "-resource-group": side.group,
		} {
			if cmd.Flags().Changed(flag) && strings.TrimSpace(value) == "" {
				return commandError("empty_copy_selector", "Copy resource selectors cannot be explicitly empty.",
					"Omit unused selectors or specify a nonempty value.")
			}
		}
		if len(args) > i {
			if cmd.Flags().Changed(name) {
				return commandError("conflicting_copy_id", "Specify each analyzer ID only once.",
					"Use positional IDs or their corresponding named flags.")
			}
			side.id = args[i]
		}
		side.id = strings.TrimSpace(side.id)
		if err := cuclient.ValidateAnalyzerID(side.id); err != nil {
			return err
		}
		if side.group != "" && side.resource == "" {
			return commandError("missing_copy_resource", "A resource-group selector requires its resource selector.",
				"Specify --"+name+"-resource.")
		}
		if name == "destination" && side.subscription != "" && side.resource == "" {
			return commandError("missing_copy_resource",
				"A destination subscription requires a destination resource selector.",
				"Specify --destination-resource; omitting it means the resolved source resource.")
		}
		if side.resource != "" {
			if _, err := armclient.ParseSelector(
				side.resource, side.subscription, side.group, config.ResourceScope{}); err != nil {
				return err
			}
		}
	}
	discover := source.resource != "" || source.subscription != "" ||
		destination.resource != "" || destination.subscription != ""
	if !discover {
		if err := (cuclient.CopyAnalyzerRequest{
			SourceAnalyzerID: source.id, DestinationAnalyzerID: destination.id,
		}).Validate(); err != nil {
			return err
		}
	}
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	session, err := open(cmd, extCtx, options)
	if err != nil {
		return err
	}
	defer session.close()
	sourceClient, destinationClient := session.client, session.client
	request := analyzercopy.Request{SourceID: source.id, DestinationID: destination.id}
	if discover {
		if session.resolveAccount == nil || session.clientForEndpoint == nil {
			return commandError("copy_discovery_unavailable", "The current service session cannot resolve copy resources.",
				"Retry through the azd extension host.")
		}
		if source.resource == "" {
			source.resource = session.endpoint
		}
		sourceAccount, err := session.resolveAccount(cmd.Context(), source.resource, source.subscription, source.group)
		if err != nil {
			return err
		}
		destinationAccount := sourceAccount
		if destination.resource != "" {
			destinationAccount, err = session.resolveAccount(
				cmd.Context(), destination.resource, destination.subscription, destination.group)
			if err != nil {
				return err
			}
		}
		if sourceClient, err = session.clientForEndpoint(sourceAccount.Endpoint); err != nil {
			return err
		}
		destinationClient = sourceClient
		if !strings.EqualFold(sourceAccount.ID, destinationAccount.ID) {
			request.SourceResourceID, request.SourceRegion = sourceAccount.ID, sourceAccount.Region
			request.DestinationResourceID, request.DestinationRegion = destinationAccount.ID, destinationAccount.Region
			if destinationClient, err = session.clientForEndpoint(destinationAccount.Endpoint); err != nil {
				return err
			}
		}
	}
	result, err := analyzercopy.Copy(cmd.Context(), sourceClient, destinationClient, request)
	if err != nil {
		return err
	}
	raw, err := analyzerJSON(result.Analyzer)
	if err != nil {
		return err
	}
	return writeServiceResult(cmd, extCtx.OutputFormat, raw)
}
