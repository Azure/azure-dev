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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// --- LIST ---

func newConnectionListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var kind string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List connections in the Foundry project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			connCtx, err := resolveConnectionContext(ctx, cmd)
			if err != nil {
				return err
			}

			pager := connCtx.armClient.NewListPager(
				connCtx.rg, connCtx.account, connCtx.project, nil,
			)

			var results []connectionListItem
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return exterrors.ServiceFromAzure(err, exterrors.OpListConnections)
				}
				for _, conn := range page.Value {
					props := conn.Properties.GetConnectionPropertiesV2()
					if props == nil {
						continue
					}
					if kind != "" && props.Category != nil && string(*props.Category) != kind {
						continue
					}
					results = append(results, connectionListItem{
						Name:     deref(conn.Name),
						Kind:     categoryStr(props.Category),
						AuthType: authTypeStr(props.AuthType),
						Target:   deref(props.Target),
					})
				}
			}

			return printList(results, extCtx.OutputFormat)
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "", "Filter by connection kind (e.g., RemoteTool)")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

// --- SHOW ---

func newConnectionShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var showCredentials bool

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show connection details.",
		Long:  "Show connection details. Use --show-credentials to fetch secret values.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			ctx := azdext.WithAccessToken(cmd.Context())

			connCtx, err := resolveConnectionContext(ctx, cmd)
			if err != nil {
				return err
			}

			armResp, err := connCtx.armClient.Get(
				ctx, connCtx.rg, connCtx.account, connCtx.project, name, nil,
			)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpGetConnection)
			}

			props := armResp.Properties.GetConnectionPropertiesV2()
			result := connectionDetailResult{
				Name:     deref(armResp.Name),
				Kind:     categoryStr(props.Category),
				AuthType: authTypeStr(props.AuthType),
				Target:   deref(props.Target),
				Metadata: props.Metadata,
			}

			if showCredentials {
				dpConn, dpErr := connCtx.dpClient.GetConnectionWithCredentials(ctx, name)
				if dpErr != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not fetch credentials: %s\n", dpErr)
				} else {
					result.Credentials = dpConn.Credentials
					result.CredentialRefs = buildCredentialReferences(name, dpConn.Credentials)
				}
			}

			return printDetail(result, extCtx.OutputFormat)
		},
	}

	cmd.Flags().BoolVar(&showCredentials, "show-credentials", false,
		"Fetch credential values from the data plane")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

// --- CREATE ---

func newConnectionCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var (
		kind       string
		target     string
		authType   string
		key        string
		customKeys []string
		metadata   []string
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new Foundry project connection.",
		Example: `  azd ai connection create my-search \
    --kind CognitiveSearch --target https://my-search.search.windows.net/ \
    --auth-type ApiKey --key "abc123..."

  azd ai connection create my-tavily \
    --kind RemoteTool --target https://mcp.tavily.com/mcp \
    --auth-type CustomKeys --custom-key "x-api-key=tvly-abc123"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			ctx := azdext.WithAccessToken(cmd.Context())

			connCtx, err := resolveConnectionContext(ctx, cmd)
			if err != nil {
				return err
			}

			// Pre-check: fail if connection exists and --force not set
			if !force {
				if _, err := connCtx.armClient.Get(
					ctx, connCtx.rg, connCtx.account, connCtx.project, name, nil,
				); err == nil {
					return exterrors.Validation(
						exterrors.CodeConnectionAlreadyExists,
						fmt.Sprintf("Connection %q already exists.", name),
						"Use --force to replace the existing connection.",
					)
				}
			}

			body, err := buildConnectionBody(kind, target, authType, key, customKeys, metadata)
			if err != nil {
				return err
			}

			_, err = connCtx.armClient.Create(
				ctx, connCtx.rg, connCtx.account, connCtx.project, name,
				&armcognitiveservices.ProjectConnectionsClientCreateOptions{
					Connection: body,
				},
			)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpCreateConnection)
			}

			fmt.Printf("Connection %q created in project %q.\n", name, connCtx.project)
			return nil
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "", "Connection kind (e.g., RemoteTool, CognitiveSearch)")
	cmd.Flags().StringVar(&target, "target", "", "Target URL or ARM resource ID")
	cmd.Flags().StringVar(&authType, "auth-type", "None", "Auth type: ApiKey, CustomKeys, None")
	cmd.Flags().StringVar(&key, "key", "", "API key (for ApiKey auth)")
	cmd.Flags().StringArrayVar(&customKeys, "custom-key", nil, "Custom key=value (repeatable)")
	cmd.Flags().StringArrayVar(&metadata, "metadata", nil, "Metadata key=value (repeatable)")
	cmd.Flags().BoolVar(&force, "force", false, "Replace existing connection (upsert)")
	return cmd
}

// --- DELETE ---

func newConnectionDeleteCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a connection.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			ctx := azdext.WithAccessToken(cmd.Context())

			connCtx, err := resolveConnectionContext(ctx, cmd)
			if err != nil {
				return err
			}

			resp, err := connCtx.armClient.Get(
				ctx, connCtx.rg, connCtx.account, connCtx.project, name, nil,
			)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpGetConnection)
			}

			props := resp.Properties.GetConnectionPropertiesV2()
			fmt.Printf("Connection: %s (%s)\n", name, categoryStr(props.Category))
			fmt.Printf("Target:     %s\n", deref(props.Target))

			if !force {
				if extCtx.NoPrompt {
					return exterrors.Validation(
						exterrors.CodeMissingForceFlag,
						fmt.Sprintf("Deleting %q requires confirmation.", name),
						"Use --force to skip confirmation in non-interactive mode.",
					)
				}
				azdClient, err := azdext.NewAzdClient()
				if err != nil {
					return fmt.Errorf("failed to create azd client: %w", err)
				}
				defer azdClient.Close()

				confirmResp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
					Options: &azdext.ConfirmOptions{
						Message:      "Are you sure you want to delete this connection?",
						DefaultValue: new(false),
					},
				})
				if err != nil {
					return err
				}
				if !*confirmResp.Value {
					fmt.Println("Cancelled.")
					return nil
				}
			}

			_, err = connCtx.armClient.Delete(
				ctx, connCtx.rg, connCtx.account, connCtx.project, name, nil,
			)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpDeleteConnection)
			}

			fmt.Printf("Connection %q deleted.\n", name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	return cmd
}

// --- Helpers ---

type connectionListItem struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	AuthType string `json:"authType"`
	Target   string `json:"target"`
}

type connectionDetailResult struct {
	Name           string                            `json:"name"`
	Kind           string                            `json:"kind"`
	AuthType       string                            `json:"authType"`
	Target         string                            `json:"target"`
	Metadata       map[string]*string                `json:"metadata,omitempty"`
	Credentials    *connections.ConnectionCredentials `json:"credentials,omitempty"`
	CredentialRefs map[string]string                 `json:"credentialReferences,omitempty"`
}

func buildCredentialReferences(
	connName string, creds *connections.ConnectionCredentials,
) map[string]string {
	if creds == nil {
		return nil
	}
	refs := map[string]string{}
	if creds.Key != "" {
		refs["key"] = fmt.Sprintf("${{connections.%s.credentials.key}}", connName)
	}
	for k := range creds.CustomKeys {
		refs[k] = fmt.Sprintf("${{connections.%s.credentials.%s}}", connName, k)
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
}

func buildConnectionBody(
	kind, target, authType, key string,
	customKeys, metadata []string,
) (*armcognitiveservices.ConnectionPropertiesV2BasicResource, error) {
	metaMap := parseKVPtrMap(metadata)
	cat := armcognitiveservices.ConnectionCategory(kind)
	at := armcognitiveservices.ConnectionAuthType(authType)

	switch authType {
	case "ApiKey":
		return &armcognitiveservices.ConnectionPropertiesV2BasicResource{
			Properties: &armcognitiveservices.APIKeyAuthConnectionProperties{
				AuthType:    &at,
				Category:    &cat,
				Target:      &target,
				Credentials: &armcognitiveservices.ConnectionAPIKey{Key: &key},
				Metadata:    metaMap,
			},
		}, nil

	case "CustomKeys":
		keysMap := parseKVPtrMap(customKeys)
		return &armcognitiveservices.ConnectionPropertiesV2BasicResource{
			Properties: &armcognitiveservices.CustomKeysConnectionProperties{
				AuthType:    &at,
				Category:    &cat,
				Target:      &target,
				Credentials: &armcognitiveservices.CustomKeys{Keys: keysMap},
				Metadata:    metaMap,
			},
		}, nil

	case "None", "":
		noneAuth := armcognitiveservices.ConnectionAuthTypeNone
		return &armcognitiveservices.ConnectionPropertiesV2BasicResource{
			Properties: &armcognitiveservices.NoneAuthTypeConnectionProperties{
				AuthType: &noneAuth,
				Category: &cat,
				Target:   &target,
				Metadata: metaMap,
			},
		}, nil

	default:
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAuthType,
			fmt.Sprintf("Unsupported auth type %q.", authType),
			"Supported: ApiKey, CustomKeys, None",
		)
	}
}

func printList(items []connectionListItem, format string) error {
	if format == "json" {
		data, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "Name\tKind\tAuth Type\tTarget")
	fmt.Fprintln(w, "----\t----\t---------\t------")
	for _, item := range items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.Name, item.Kind, item.AuthType, item.Target)
	}
	return w.Flush()
}

func printDetail(result connectionDetailResult, format string) error {
	if format == "json" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("Name:      %s\n", result.Name)
	fmt.Printf("Kind:      %s\n", result.Kind)
	fmt.Printf("Auth Type: %s\n", result.AuthType)
	fmt.Printf("Target:    %s\n", result.Target)
	if result.Credentials != nil {
		fmt.Println("\nCredentials:")
		if result.Credentials.Key != "" {
			fmt.Printf("  key: %s\n", result.Credentials.Key)
		}
		for k, v := range result.Credentials.CustomKeys {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
	if len(result.CredentialRefs) > 0 {
		fmt.Println("\nCredential References (for agent.yaml):")
		for k, v := range result.CredentialRefs {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
	return nil
}

func parseKVPtrMap(pairs []string) map[string]*string {
	if len(pairs) == 0 {
		return nil
	}
	result := make(map[string]*string, len(pairs))
	for _, pair := range pairs {
		for i := range len(pair) {
			if pair[i] == '=' {
				v := pair[i+1:]
				result[pair[:i]] = &v
				break
			}
		}
	}
	return result
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func categoryStr(c *armcognitiveservices.ConnectionCategory) string {
	if c == nil {
		return ""
	}
	return string(*c)
}

func authTypeStr(a *armcognitiveservices.ConnectionAuthType) string {
	if a == nil {
		return ""
	}
	return string(*a)
}
