// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/spf13/cobra"
)

// newRoutineClient resolves the project endpoint and creates an authenticated routine client.
func newRoutineClient(ctx context.Context, cmd *cobra.Command) (*routines.Client, string, error) {
	flagEndpoint, _ := cmd.Flags().GetString("project-endpoint")

	resolved, err := resolveProjectEndpoint(ctx, flagEndpoint)
	if err != nil {
		return nil, "", err
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, "", exterrors.Auth(
			exterrors.CodeAuthFailed,
			fmt.Sprintf("failed to create Azure credential: %v", err),
			"run `azd auth login` to authenticate",
		)
	}

	return routines.NewClient(resolved.Endpoint, cred), resolved.Endpoint, nil
}

// printJSON marshals v to indented JSON and writes to stdout.
func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON output: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// newTabWriter creates a tabwriter that flushes to stdout.
func newTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}

// boolStr returns "true"/"false" for a *bool pointer.
func boolStr(b *bool) string {
	if b == nil {
		return "true"
	}
	if *b {
		return "true"
	}
	return "false"
}

// routineSummaryTable prints a short summary of a routine in table format.
func routineSummaryTable(r *routines.Routine) {
	tw := newTabWriter()
	defer tw.Flush()
	fmt.Fprintf(tw, "Name:\t%s\n", r.Name)
	if r.Description != "" {
		fmt.Fprintf(tw, "Description:\t%s\n", r.Description)
	}
	fmt.Fprintf(tw, "Enabled:\t%s\n", boolStr(r.Enabled))
	if t, ok := r.Triggers[routines.DefaultTriggerKey]; ok {
		fmt.Fprintf(tw, "Trigger:\t%s\n", t.Type)
		if t.Cron != "" {
			fmt.Fprintf(tw, "  Cron:\t%s\n", t.Cron)
		}
		if t.At != "" {
			fmt.Fprintf(tw, "  At:\t%s\n", t.At)
		}
		if t.TimeZone != "" {
			fmt.Fprintf(tw, "  TimeZone:\t%s\n", t.TimeZone)
		}
	}
	if a, ok := r.Actions[routines.DefaultActionKey]; ok {
		fmt.Fprintf(tw, "Action:\t%s\n", a.Type)
		if a.AgentName != "" {
			fmt.Fprintf(tw, "  AgentName:\t%s\n", a.AgentName)
		}
		if a.AgentEndpointID != "" {
			fmt.Fprintf(tw, "  AgentEndpointID:\t%s\n", a.AgentEndpointID)
		}
	}
}
