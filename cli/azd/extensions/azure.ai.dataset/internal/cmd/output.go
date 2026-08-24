// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"azureaidataset/internal/messages"

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

// noPrompt reports whether the caller asked for no interaction.
//
// JSON output counts: a question written into a document nobody is reading is a
// hang rather than a prompt.
func noPrompt(cmd *cobra.Command) bool {
	if isJSON(cmd) {
		return true
	}
	value, err := cmd.Flags().GetBool("no-prompt")
	return err == nil && value
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

// emitTable writes a list view: uppercase headers over a rule, tab-aligned.
//
// The rule is what separates the header from the data at a glance, and it is
// what `azure.ai.skills` prints, so a reader moving between the Foundry
// extensions sees one table.
func emitTable(w io.Writer, headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
		return err
	}
	rule := make([]string, len(headers))
	for i, h := range headers {
		rule[i] = strings.Repeat("-", len(h))
	}
	if _, err := fmt.Fprintln(tw, strings.Join(rule, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// field is one row of a detail view.
type field struct {
	Key   string // Title Case, per the azd style guide
	Value string
}

// emitDetail writes a two-column key/value view, the shape `show` uses.
//
// Empty values are dropped rather than printed blank: a detail view is read to
// learn what a thing is, and a column of empty keys says only that the writer
// did not know which fields this kind has.
func emitDetail(w io.Writer, fields []field) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	for _, f := range fields {
		if f.Value == "" {
			continue
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", f.Key, f.Value); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// requireFlag returns an error naming a flag the command needs and has no way
// to settle for itself.
func requireFlag(name string) error {
	return messages.FlagRequired(name)
}
