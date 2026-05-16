// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"text/tabwriter"

	"azureaiagent/internal/connections/exterrors"
	"azureaiagent/internal/connections/pkg/connections"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// --- LIST ---

// connectionListFlags holds validated input for ConnectionListAction.
type connectionListFlags struct {
	kind            string
	output          string
	projectEndpoint string
}

// ConnectionListAction implements connection listing.
type ConnectionListAction struct {
	flags *connectionListFlags
}

// Run executes the list operation.
func (a *ConnectionListAction) Run(ctx context.Context) error {
	normalizedKind := normalizeKind(a.flags.kind)

	connCtx, err := resolveConnectionContext(ctx, a.flags.projectEndpoint)
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
			if normalizedKind != "" &&
				(props.Category == nil || string(*props.Category) != normalizedKind) {
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

	return printList(results, a.flags.output)
}

func newConnectionListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &connectionListFlags{}
	action := &ConnectionListAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List connections in the Foundry project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.output = extCtx.OutputFormat
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")

			ctx := azdext.WithAccessToken(cmd.Context())
			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&flags.kind, "kind", "",
		"Filter by connection kind (e.g., remote-tool)")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

// --- SHOW ---

// connectionShowFlags holds validated input for ConnectionShowAction.
type connectionShowFlags struct {
	name            string
	showCredentials bool
	output          string
	projectEndpoint string
}

// ConnectionShowAction implements connection show.
type ConnectionShowAction struct {
	flags *connectionShowFlags
}

// Run executes the show operation.
func (a *ConnectionShowAction) Run(ctx context.Context) error {
	connCtx, err := resolveConnectionContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}

	armResp, err := connCtx.armClient.Get(
		ctx, connCtx.rg, connCtx.account, connCtx.project, a.flags.name, nil,
	)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetConnection)
	}

	props := armResp.Properties.GetConnectionPropertiesV2()
	if props == nil {
		return fmt.Errorf("connection %q: unexpected response format", a.flags.name)
	}

	result := connectionDetailResult{
		Name:     deref(armResp.Name),
		Kind:     categoryStr(props.Category),
		AuthType: authTypeStr(props.AuthType),
		Target:   deref(props.Target),
		Metadata: props.Metadata,
	}

	if a.flags.showCredentials {
		dpConn, dpErr := connCtx.dpClient.GetConnectionWithCredentials(
			ctx, a.flags.name,
		)
		if dpErr != nil {
			fmt.Fprintf(os.Stderr,
				"Warning: could not fetch credentials: %s\n", dpErr)
		} else if dpConn.Credentials != nil {
			result.Credentials = dpConn.Credentials.RawFields
			result.CredentialRefs = buildCredentialReferences(
				a.flags.name, dpConn.Credentials,
			)
		}
	}

	return printDetail(result, a.flags.output)
}

func newConnectionShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &connectionShowFlags{}
	action := &ConnectionShowAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show connection details.",
		Long:  "Show connection details. Use --show-credentials to fetch secret values.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")

			ctx := azdext.WithAccessToken(cmd.Context())
			return action.Run(ctx)
		},
	}

	cmd.Flags().BoolVar(&flags.showCredentials, "show-credentials", false,
		"Fetch credential values from the data plane")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

// --- CREATE ---

// connectionCreateFlags holds validated input for ConnectionCreateAction.
type connectionCreateFlags struct {
	name            string
	kind            string
	target          string
	authType        string
	key             string
	customKeys      []string
	metadata        []string
	force           bool
	projectEndpoint string
}

// ConnectionCreateAction implements connection creation.
type ConnectionCreateAction struct {
	flags *connectionCreateFlags
}

// Run executes the create operation.
func (a *ConnectionCreateAction) Run(ctx context.Context) error {
	if a.flags.kind == "" {
		return exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"Missing required flag --kind.",
			"Specify the connection kind (e.g., --kind remote-tool).",
		)
	}
	if a.flags.target == "" {
		return exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"Missing required flag --target.",
			"Specify the target URL (e.g., --target https://example.com).",
		)
	}
	if a.flags.authType == "api-key" && a.flags.key == "" {
		return exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"Missing required flag --key for api-key auth.",
			"Specify the API key value.",
		)
	}
	if a.flags.authType == "custom-keys" && len(a.flags.customKeys) == 0 {
		return exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"Missing required flag --custom-key for custom-keys auth.",
			"Specify at least one custom key (e.g., --custom-key x-api-key=value).",
		)
	}

	connCtx, err := resolveConnectionContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}

	// Pre-check: fail if connection exists and --force not set
	if !a.flags.force {
		if _, err := connCtx.armClient.Get(
			ctx, connCtx.rg, connCtx.account, connCtx.project,
			a.flags.name, nil,
		); err == nil {
			return exterrors.Validation(
				exterrors.CodeConnectionAlreadyExists,
				fmt.Sprintf("Connection %q already exists.", a.flags.name),
				"Use --force to replace the existing connection.",
			)
		}
	}

	body, err := buildConnectionBody(
		a.flags.kind, a.flags.target, a.flags.authType,
		a.flags.key, a.flags.customKeys, a.flags.metadata,
	)
	if err != nil {
		return err
	}

	_, err = connCtx.armClient.Create(
		ctx, connCtx.rg, connCtx.account, connCtx.project,
		a.flags.name,
		&armcognitiveservices.ProjectConnectionsClientCreateOptions{
			Connection: body,
		},
	)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateConnection)
	}

	fmt.Printf("Connection %q created in project %q.\n",
		a.flags.name, connCtx.project)
	return nil
}

func newConnectionCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &connectionCreateFlags{}
	action := &ConnectionCreateAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new Foundry project connection.",
		Example: `  azd ai connection create my-search \
    --kind cognitive-search --target https://my-search.search.windows.net/ \
    --auth-type api-key --key "abc123..."

  azd ai connection create my-tavily \
    --kind remote-tool --target https://mcp.tavily.com/mcp \
    --auth-type custom-keys --custom-key "x-api-key=tvly-abc123"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")

			ctx := azdext.WithAccessToken(cmd.Context())
			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&flags.kind, "kind", "",
		"Connection kind (e.g., remote-tool, cognitive-search)")
	cmd.Flags().StringVar(&flags.target, "target", "",
		"Target URL or ARM resource ID")
	cmd.Flags().StringVar(&flags.authType, "auth-type", "none",
		"Auth type: api-key, custom-keys, none")
	cmd.Flags().StringVar(&flags.key, "key", "",
		"API key (for api-key auth)")
	cmd.Flags().StringArrayVar(&flags.customKeys, "custom-key", nil,
		"Custom key=value (repeatable, for custom-keys auth)")
	cmd.Flags().StringArrayVar(&flags.metadata, "metadata", nil,
		"Metadata key=value (repeatable)")
	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Replace existing connection (upsert)")
	return cmd
}

// --- UPDATE ---

// connectionUpdateFlags holds validated input for ConnectionUpdateAction.
type connectionUpdateFlags struct {
	name             string
	target           string
	key              string
	customKeys       []string
	targetChanged    bool
	keyChanged       bool
	customKeyChanged bool
	projectEndpoint  string
}

// ConnectionUpdateAction implements connection update.
type ConnectionUpdateAction struct {
	flags *connectionUpdateFlags
}

// Run executes the update operation.
func (a *ConnectionUpdateAction) Run(ctx context.Context) error {
	if !a.flags.targetChanged && !a.flags.keyChanged &&
		!a.flags.customKeyChanged {
		return exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"No fields to update.",
			"Specify --target, --key, or --custom-key.",
		)
	}

	connCtx, err := resolveConnectionContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}

	// GET current connection metadata from ARM
	current, err := connCtx.armClient.Get(
		ctx, connCtx.rg, connCtx.account, connCtx.project,
		a.flags.name, nil,
	)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetConnection)
	}

	// Fetch current credentials from data-plane (ARM never returns credentials)
	dpConn, err := connCtx.dpClient.GetConnectionWithCredentials(
		ctx, a.flags.name,
	)
	if err != nil {
		return fmt.Errorf("failed to fetch current credentials: %w", err)
	}

	props := current.Properties.GetConnectionPropertiesV2()

	// Apply target change
	newTarget := deref(props.Target)
	if a.flags.targetChanged {
		newTarget = a.flags.target
	}

	// Build merged credentials
	newKey := ""
	newCustomKeys := map[string]string{}
	if dpConn.Credentials != nil {
		newKey = dpConn.Credentials.Key
		maps.Copy(newCustomKeys, dpConn.Credentials.CustomKeys)
	}
	if a.flags.keyChanged {
		newKey = a.flags.key
	}
	if a.flags.customKeyChanged {
		for _, kv := range a.flags.customKeys {
			for i := range len(kv) {
				if kv[i] == '=' {
					newCustomKeys[kv[:i]] = kv[i+1:]
					break
				}
			}
		}
	}

	// Rebuild the full connection body with credentials
	normalizedAuth := normalizeAuthType(authTypeStr(props.AuthType))
	kindStr := categoryStr(props.Category)
	metaPairs := []string{}
	for k, v := range props.Metadata {
		if v != nil {
			metaPairs = append(metaPairs, k+"="+*v)
		}
	}

	var credKey string
	var credCustomKeys []string
	if newKey != "" {
		credKey = newKey
	}
	for k, v := range newCustomKeys {
		credCustomKeys = append(credCustomKeys, k+"="+v)
	}

	body, err := buildConnectionBody(
		kindStr, newTarget, normalizedAuth,
		credKey, credCustomKeys, metaPairs,
	)
	if err != nil {
		return err
	}

	_, err = connCtx.armClient.Create(
		ctx, connCtx.rg, connCtx.account, connCtx.project,
		a.flags.name,
		&armcognitiveservices.ProjectConnectionsClientCreateOptions{
			Connection: body,
		},
	)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpUpdateConnection)
	}

	fmt.Printf("Connection %q updated.\n", a.flags.name)
	return nil
}

func newConnectionUpdateCommand(
	extCtx *azdext.ExtensionContext,
) *cobra.Command {
	flags := &connectionUpdateFlags{}
	action := &ConnectionUpdateAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a connection's target or credentials.",
		Long: `Update a connection's target URL or credential values.

Only the specified flags are changed; all other fields are preserved.
Does not accept --auth-type (delete and recreate to change auth type).
For metadata changes, use the 'metadata' subcommand.`,
		Example: `  azd ai agent connection update prod-search --key "$NEW_SEARCH_KEY"
  azd ai agent connection update my-conn --target https://new-endpoint.com
  azd ai agent connection update my-mcp --custom-key "x-api-key=new-key"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			flags.targetChanged = cmd.Flags().Changed("target")
			flags.keyChanged = cmd.Flags().Changed("key")
			flags.customKeyChanged = cmd.Flags().Changed("custom-key")

			ctx := azdext.WithAccessToken(cmd.Context())
			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&flags.target, "target", "",
		"New target URL or ARM resource ID")
	cmd.Flags().StringVar(&flags.key, "key", "",
		"New API key value (for api-key auth)")
	cmd.Flags().StringArrayVar(&flags.customKeys, "custom-key", nil,
		"Update custom key=value (repeatable, for custom-keys auth)")
	return cmd
}

// --- DELETE ---

// connectionDeleteFlags holds validated input for ConnectionDeleteAction.
type connectionDeleteFlags struct {
	name            string
	force           bool
	noPrompt        bool
	projectEndpoint string
}

// ConnectionDeleteAction implements connection deletion.
type ConnectionDeleteAction struct {
	flags *connectionDeleteFlags
}

// Run executes the delete operation.
func (a *ConnectionDeleteAction) Run(ctx context.Context) error {
	connCtx, err := resolveConnectionContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}

	resp, err := connCtx.armClient.Get(
		ctx, connCtx.rg, connCtx.account, connCtx.project,
		a.flags.name, nil,
	)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetConnection)
	}

	props := resp.Properties.GetConnectionPropertiesV2()
	fmt.Printf("Connection: %s (%s)\n",
		a.flags.name, categoryStr(props.Category))
	fmt.Printf("Target:     %s\n", deref(props.Target))

	if !a.flags.force {
		if a.flags.noPrompt {
			return exterrors.Validation(
				exterrors.CodeMissingForceFlag,
				fmt.Sprintf(
					"Deleting %q requires confirmation.", a.flags.name,
				),
				"Use --force to skip confirmation in non-interactive mode.",
			)
		}
		azdClient, err := azdext.NewAzdClient()
		if err != nil {
			return fmt.Errorf("failed to create azd client: %w", err)
		}
		defer azdClient.Close()

		confirmResp, err := azdClient.Prompt().Confirm(
			ctx, &azdext.ConfirmRequest{
				Options: &azdext.ConfirmOptions{
					Message:      "Are you sure you want to delete this connection?",
					DefaultValue: new(false),
				},
			},
		)
		if err != nil {
			return err
		}
		if !*confirmResp.Value {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	_, err = connCtx.armClient.Delete(
		ctx, connCtx.rg, connCtx.account, connCtx.project,
		a.flags.name, nil,
	)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpDeleteConnection)
	}

	fmt.Printf("Connection %q deleted.\n", a.flags.name)
	return nil
}

func newConnectionDeleteCommand(
	extCtx *azdext.ExtensionContext,
) *cobra.Command {
	flags := &connectionDeleteFlags{}
	action := &ConnectionDeleteAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a connection.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.noPrompt = extCtx.NoPrompt
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")

			ctx := azdext.WithAccessToken(cmd.Context())
			return action.Run(ctx)
		},
	}

	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Skip confirmation prompt")
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
	Name           string             `json:"name"`
	Kind           string             `json:"kind"`
	AuthType       string             `json:"authType"`
	Target         string             `json:"target"`
	Metadata       map[string]*string `json:"metadata,omitempty"`
	Credentials    map[string]string  `json:"credentials,omitempty"`
	CredentialRefs map[string]string  `json:"credentialReferences,omitempty"`
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
	cat := armcognitiveservices.ConnectionCategory(normalizeKind(kind))

	// Map CLI kebab-case auth types to ARM SDK values
	switch authType {
	case "api-key":
		at := armcognitiveservices.ConnectionAuthTypeAPIKey
		return &armcognitiveservices.ConnectionPropertiesV2BasicResource{
			Properties: &armcognitiveservices.APIKeyAuthConnectionProperties{
				AuthType:    &at,
				Category:    &cat,
				Target:      &target,
				Credentials: &armcognitiveservices.ConnectionAPIKey{Key: &key},
				Metadata:    metaMap,
			},
		}, nil

	case "custom-keys":
		at := armcognitiveservices.ConnectionAuthTypeCustomKeys
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

	case "none", "":
		at := armcognitiveservices.ConnectionAuthTypeNone
		return &armcognitiveservices.ConnectionPropertiesV2BasicResource{
			Properties: &armcognitiveservices.NoneAuthTypeConnectionProperties{
				AuthType: &at,
				Category: &cat,
				Target:   &target,
				Metadata: metaMap,
			},
		}, nil

	default:
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAuthType,
			fmt.Sprintf("Unsupported auth type %q.", authType),
			"Supported: api-key, custom-keys, none",
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
	if len(result.Credentials) > 0 {
		fmt.Println("\nCredentials:")
		for k, v := range result.Credentials {
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
		found := false
		for i := range len(pair) {
			if pair[i] == '=' {
				v := pair[i+1:]
				result[pair[:i]] = &v
				found = true
				break
			}
		}
		if !found {
			log.Printf("warning: ignoring malformed key=value pair: %q", pair)
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

func normalizeKind(cliKind string) string {
	mapping := map[string]string{
		"remote-tool":                "RemoteTool",
		"cognitive-search":           "CognitiveSearch",
		"api-key":                    "ApiKey",
		"app-insights":               "AppInsights",
		"grounding-with-bing-search": "GroundingWithBingSearch",
		"ai-services":                "AIServices",
		"container-registry":         "ContainerRegistry",
		"custom-keys":                "CustomKeys",
	}
	if mapped, ok := mapping[cliKind]; ok {
		return mapped
	}
	return cliKind
}

// normalizeAuthType converts ARM SDK auth type values to CLI kebab-case format.
func normalizeAuthType(armAuthType string) string {
	switch armAuthType {
	case "ApiKey":
		return "api-key"
	case "CustomKeys":
		return "custom-keys"
	case "None":
		return "none"
	default:
		return armAuthType
	}
}
