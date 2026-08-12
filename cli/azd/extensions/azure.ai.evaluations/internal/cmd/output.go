// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

const outputJSON = "json"

// writePortalLink closes a detail view with the asset's portal URL.
//
// Last line and cyan, matching the sibling extensions, and silent when there is
// no URL — the link is a convenience on top of work already done, so its
// absence must not look like a failure.
func writePortalLink(w io.Writer, url string) {
	if url == "" {
		return
	}
	fmt.Fprint(w, messages.PortalLink(color.CyanString(url)))
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

// noPrompt reports whether the caller asked for no interaction.
//
// JSON output counts: a prompt written into a document nobody is reading is a
// hang, not a question.
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

// requireFlag returns an error naming the missing flag, used when --no-prompt
// prevents asking for a required value.
func requireFlag(name string) error {
	return messages.FlagRequired(name)
}

// writeFileAtomic replaces a file's contents in one step.
//
// The caller is usually overwriting a definition the developer already has and
// wants to keep working with, so a half-written file is worse than no write at
// all: os.WriteFile truncates first, and a failure after that leaves the good
// local copy destroyed.
//
// Every error names the path the caller passed. The temporary file is this
// function's business and appears nowhere the caller asked for.
func writeFileAtomic(path string, body []byte) error {
	// Refuse anything that is not a regular file: pointed at a directory, the
	// replacement below would report a confusing rename failure instead.
	switch info, err := os.Stat(path); {
	case err == nil && !info.Mode().IsRegular():
		return messages.NotARegularFile(path)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return messages.Creating(path, err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".azd-eval-*")
	if err != nil {
		return messages.CannotWriteInDirectory(dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return messages.Creating(path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return messages.Creating(path, err)
	}
	if err := tmp.Close(); err != nil {
		return messages.Creating(path, err)
	}
	if err := project.ReplaceFile(tmpName, path); err != nil {
		return messages.Creating(path, err)
	}
	return nil
}
