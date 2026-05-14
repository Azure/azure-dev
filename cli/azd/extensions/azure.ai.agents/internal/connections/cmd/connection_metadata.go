// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"azureaiagent/internal/connections/exterrors"
	"azureaiagent/internal/connections/pkg/connections"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newConnectionMetadataCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metadata <command>",
		Short: "Manage connection metadata key-value pairs.",
	}

	cmd.AddCommand(newMetadataSetCommand(extCtx))
	cmd.AddCommand(newMetadataRemoveCommand(extCtx))
	cmd.AddCommand(newMetadataListCommand(extCtx))

	return cmd
}

func newMetadataSetCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return &cobra.Command{
		Use:   "set <connection-name> <key=value>",
		Short: "Set a metadata key-value pair on a connection.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			connName, kv := args[0], args[1]
			ctx := azdext.WithAccessToken(cmd.Context())

			var k, v string
			for i := range len(kv) {
				if kv[i] == '=' {
					k, v = kv[:i], kv[i+1:]
					break
				}
			}
			if k == "" {
				return exterrors.Validation(
					exterrors.CodeMissingConnectionField,
					"Invalid key=value format.",
					"Use: azd ai agent connection metadata set <name> <key=value>",
				)
			}

			ep, _ := cmd.Flags().GetString("project-endpoint")
			connCtx, err := resolveConnectionContext(ctx, ep)
			if err != nil {
				return err
			}

			err = rebuildAndPutConnection(ctx, connCtx, connName,
				func(props *armcognitiveservices.ConnectionPropertiesV2, _ *connections.ConnectionCredentials) {
					if props.Metadata == nil {
						props.Metadata = map[string]*string{}
					}
					props.Metadata[k] = &v
				},
			)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpSetConnectionMetadata)
			}

			fmt.Printf("Metadata %q set on connection %q.\n", k, connName)
			return nil
		},
	}
}

func newMetadataRemoveCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <connection-name> <key>",
		Short: "Remove a metadata key from a connection.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			connName, key := args[0], args[1]
			ctx := azdext.WithAccessToken(cmd.Context())

			ep, _ := cmd.Flags().GetString("project-endpoint")
			connCtx, err := resolveConnectionContext(ctx, ep)
			if err != nil {
				return err
			}

			err = rebuildAndPutConnection(ctx, connCtx, connName,
				func(props *armcognitiveservices.ConnectionPropertiesV2, _ *connections.ConnectionCredentials) {
					if props.Metadata != nil {
						delete(props.Metadata, key)
					}
				},
			)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpRemoveConnectionMetadata)
			}

			fmt.Printf("Metadata %q removed from connection %q.\n", key, connName)
			return nil
		},
	}
}

func newMetadataListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <connection-name>",
		Short: "List metadata on a connection.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			connName := args[0]
			ctx := azdext.WithAccessToken(cmd.Context())

			ep, _ := cmd.Flags().GetString("project-endpoint")
			connCtx, err := resolveConnectionContext(ctx, ep)
			if err != nil {
				return err
			}

			resp, err := connCtx.armClient.Get(
				ctx, connCtx.rg, connCtx.account, connCtx.project, connName, nil,
			)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpGetConnection)
			}

			props := resp.Properties.GetConnectionPropertiesV2()
			meta := props.Metadata

			if extCtx.OutputFormat == "json" {
				data, err := json.MarshalIndent(meta, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}

			if len(meta) == 0 {
				fmt.Println("No metadata.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "Key\tValue")
			fmt.Fprintln(w, "---\t-----")
			for k, v := range meta {
				fmt.Fprintf(w, "%s\t%s\n", k, deref(v))
			}
			return w.Flush()
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}
