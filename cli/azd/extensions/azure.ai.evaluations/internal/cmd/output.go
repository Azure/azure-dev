// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

const outputJSON = "json"

// humanOut is where prose goes: the caller's stdout, or nowhere under -o json.
//
// A command that narrates what it did and then emits a document put the prose
// on stdout ahead of it, so the output stopped parsing as JSON. The document
// carries the same facts as fields, so the narration is dropped rather than
// moved to stderr, where it would be noise nobody asked for.
func humanOut(cmd *cobra.Command, out io.Writer) io.Writer {
	if isJSON(cmd) {
		return io.Discard
	}
	return out
}

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

// runLink is the one link a run has.
//
// The service sends report_url and the extension builds its own portal URL, and
// the two resolve to the same page. Printing both put two labels on one
// destination with no rule a reader could infer, so the service's value wins and
// ours is the fallback that keeps the link from going missing. Callers format
// it themselves, because the three views that show it are laid out differently.
func runLink(reportURL, portalURL string) string {
	if reportURL != "" {
		return reportURL
	}
	return portalURL
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
//
// The flag is only half of it. azd folds CI detection, agent detection,
// --non-interactive and AZD_NON_INTERACTIVE into the AZD_NO_PROMPT it sets in
// the extension's environment, and never sets this cobra flag -- so reading the
// flag alone let an unattended delete take the confirm RPC's default of no,
// report the artifact left alone, and exit 0 instead of asking for --force.
func noPrompt(cmd *cobra.Command) bool {
	if isJSON(cmd) {
		return true
	}
	if azdext.DetectInteractive().NoPrompt {
		return true
	}
	value, err := cmd.Flags().GetBool("no-prompt")
	return err == nil && value
}

// commandContext is cmd.Context() with cobra's pre-Execute nil made safe.
// gRPC dereferences the context it is handed, so a nil one is a panic rather
// than a failed call.
func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// warnWriterKey carries the command's error writer to the helpers that warn.
type warnWriterKey struct{}

// withWarnWriter puts the command's error writer where a helper can find it.
//
// Warnings used to go to process-global stderr, which nothing embedding this
// can capture and which interleaves when two commands run at once. Threading a
// writer through would have touched twenty-nine constructor call sites; every
// one of them already carries the context this rides on.
func withWarnWriter(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, warnWriterKey{}, w)
}

// warnWriter is where a warning goes: the command's error writer, or stderr
// when this ran outside one.
func warnWriter(ctx context.Context) io.Writer {
	if w, ok := ctx.Value(warnWriterKey{}).(io.Writer); ok && w != nil {
		return w
	}
	return os.Stderr
}

// emitJSON writes v as indented JSON.
func emitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// jsonListPage is the envelope every machine listing answers with.
//
// A bare array cannot say that it is one page of several, so `--limit` either
// had to be ignored for `-o json` -- leaving the flag doing nothing on half the
// surface -- or had to hand a script a short list it would read as the whole
// collection. The envelope is what lets the flag work: count is what arrived,
// total_count is what there was, and continuation_token is null only on the
// last page.
type jsonListPage[T any] struct {
	Items             []T     `json:"items"`
	Count             int     `json:"count"`
	TotalCount        *int    `json:"total_count,omitempty"`
	ContinuationToken *string `json:"continuation_token"`
}

// emitJSONPage writes one page of a listing.
//
// totalCount is nil where the service does not report one; the key is then
// absent rather than zero, because "none" and "not said" are different answers.
func emitJSONPage[T any](w io.Writer, items []T, totalCount *int, continuation string) error {
	if items == nil {
		items = []T{}
	}
	page := jsonListPage[T]{Items: items, Count: len(items), TotalCount: totalCount}
	if continuation != "" {
		page.ContinuationToken = &continuation
	}
	return emitJSON(w, page)
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
	return writeFileAtomicFunc(path, func(w io.Writer) error {
		_, err := w.Write(body)
		return err
	})
}

// writeFileAtomicFunc writes whatever the caller produces, atomically.
//
// The caller writes straight into the temporary file rather than into a buffer
// this then copies: a run's export carries every evaluated row, so holding the
// whole document in memory to write it is the one case where the size is not
// the caller's to bound. Nothing appears under the destination name until the
// write finished, so a failure leaves the previous file intact rather than a
// truncated one that still parses.
func writeFileAtomicFunc(path string, produce func(io.Writer) error) error {
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

	if err := produce(tmp); err != nil {
		_ = tmp.Close()
		return messages.Writing(path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return messages.Creating(path, err)
	}
	// Close flushes, so a failure here is content that never reached the disk.
	if err := tmp.Close(); err != nil {
		return messages.Writing(path, err)
	}
	if err := project.ReplaceFile(tmpName, path); err != nil {
		return messages.Writing(path, err)
	}
	return nil
}
