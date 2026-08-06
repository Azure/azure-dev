// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

const outputJSON = "json"

// Progress markers from the azd style guide, so the extension's lines sit
// alongside core's without a second vocabulary.
const (
	doneMark    = "(✓) Done:"    // finished successfully
	skippedMark = "(-) Skipped:" // intentionally not done, not a failure
	failedMark  = "(x) Failed:"  // the step did not complete
)

// writePortalLink closes a detail view with the asset's portal URL.
//
// Last line and cyan, matching the sibling extensions, and silent when there is
// no URL — the link is a convenience on top of work already done, so its
// absence must not look like a failure.
func writePortalLink(w io.Writer, url string) {
	if url == "" {
		return
	}
	fmt.Fprintf(w, "Portal: %s\n", color.CyanString(url))
}

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

// emitJSONList writes items as a JSON array.
//
// List commands emit a bare array rather than the envelope the service replied
// with. The envelopes disagree with each other — the OpenAI-shaped APIs wrap
// results in `data`, the ARM-shaped ones in `value` — so passing them through
// would make a caller's parsing depend on which service happens to back a given
// command. They also carry paging fields that this extension does not follow,
// which would suggest there is more to fetch when there is not.
//
// A nil slice encodes as `null`, so it is normalized to an empty array: a
// caller iterating the result should see no elements, not a type error.
func emitJSONList[T any](w io.Writer, items []T) error {
	if items == nil {
		items = []T{}
	}
	return emitJSON(w, items)
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

// requireFlag returns an error naming the missing flag, used when --no-prompt
// prevents asking for a required value.
func requireFlag(name string) error {
	return fmt.Errorf("--%s is required (running with --no-prompt)", name)
}
