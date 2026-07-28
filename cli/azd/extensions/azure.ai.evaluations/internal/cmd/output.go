// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

const outputJSON = "json"

// outputFormat reads the inherited -o/--output flag.
func outputFormat(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	v, err := cmd.Flags().GetString("output")
	if err != nil {
		return ""
	}
	return strings.ToLower(v)
}

// isJSON reports whether the command should emit machine-readable output.
func isJSON(cmd *cobra.Command) bool {
	return outputFormat(cmd) == outputJSON
}

// emitJSON writes v as indented JSON.
func emitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// emitTable writes a simple aligned table. Rows must match the header width.
func emitTable(w io.Writer, headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// noPrompt reports whether the command must run without any interaction.
func noPrompt(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	v, err := cmd.Flags().GetBool("no-prompt")
	if err != nil {
		return false
	}
	return v
}

// requireFlag returns an error naming the missing flag, used when --no-prompt
// prevents asking for a required value.
func requireFlag(name string) error {
	return fmt.Errorf("--%s is required (running with --no-prompt)", name)
}
