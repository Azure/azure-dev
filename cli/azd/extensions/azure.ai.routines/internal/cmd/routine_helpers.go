// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
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

// boolStr returns a human-readable string for a *bool field.
// Returns "unknown" when the pointer is nil so callers don't silently
// display a default that wasn't actually returned by the service.
func boolStr(b *bool) string {
	if b == nil {
		return "unknown"
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
	// Routine.triggers is a map keyed by user-defined identifiers; iterate
	// in deterministic key order so multiple triggers render consistently.
	for _, key := range sortedKeys(r.Triggers) {
		t := r.Triggers[key]
		fmt.Fprintf(tw, "Trigger (%s):\t%s\n", key, t.Type)
		if t.CronExpression != "" {
			fmt.Fprintf(tw, "  Cron:\t%s\n", t.CronExpression)
		}
		if t.At != "" {
			fmt.Fprintf(tw, "  At:\t%s\n", t.At)
		}
		if t.TimeZone != "" {
			fmt.Fprintf(tw, "  TimeZone:\t%s\n", t.TimeZone)
		}
		if t.ConnectionID != "" {
			fmt.Fprintf(tw, "  ConnectionID:\t%s\n", t.ConnectionID)
		}
		if t.Owner != "" {
			fmt.Fprintf(tw, "  Owner:\t%s\n", t.Owner)
		}
		if t.Repository != "" {
			fmt.Fprintf(tw, "  Repository:\t%s\n", t.Repository)
		}
		if t.IssueEvent != "" {
			fmt.Fprintf(tw, "  IssueEvent:\t%s\n", t.IssueEvent)
		}
		if t.Provider != "" {
			fmt.Fprintf(tw, "  Provider:\t%s\n", t.Provider)
		}
		if t.EventName != "" {
			fmt.Fprintf(tw, "  EventName:\t%s\n", t.EventName)
		}
		if t.Parameters != nil && len(*t.Parameters) > 0 {
			if data, err := json.Marshal(t.Parameters); err == nil {
				fmt.Fprintf(tw, "  Parameters:\t%s\n", string(data))
			}
		}
	}
	if r.Action != nil {
		a := r.Action
		fmt.Fprintf(tw, "Action:\t%s\n", a.Type)
		if a.AgentName != "" {
			fmt.Fprintf(tw, "  AgentName:\t%s\n", a.AgentName)
		}
		if a.AgentEndpointID != "" {
			fmt.Fprintf(tw, "  AgentEndpointID:\t%s\n", a.AgentEndpointID)
		}
		if a.Conversation != "" {
			fmt.Fprintf(tw, "  Conversation:\t%s\n", a.Conversation)
		}
		if a.SessionID != "" {
			fmt.Fprintf(tw, "  SessionID:\t%s\n", a.SessionID)
		}
		if a.Input != nil {
			if data, err := json.Marshal(a.Input); err == nil {
				fmt.Fprintf(tw, "  Input:\t%s\n", string(data))
			}
		}
	}
}

// sortedKeys returns the keys of a string-keyed map in lexicographic order.
func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
