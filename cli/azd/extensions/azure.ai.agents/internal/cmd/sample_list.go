// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// sampleListFlags holds the optional filters for `azd ai agent sample list`.
type sampleListFlags struct {
	language     string
	featuredOnly bool
	templateType string
	output       string
}

// TemplateListItem is the public JSON contract for a single template emitted by
// `azd ai agent sample list -o json`.
//
// Consumers (especially AI coding agents) read this to discover which manifest
// URLs and repo URLs they can pass to `azd ai agent init -m <url>` or
// `azd init -t <url>` without scraping documentation or guessing slugs.
//
// Schema stability: fields added in future versions MUST be additive; existing
// fields keep their semantics. Consumers must ignore unknown fields.
//
// URL discriminator invariant: exactly one of ManifestURL or RepoURL is
// populated on each item; Type indicates which one:
//
//   - Type == "agent" => ManifestURL is set, RepoURL is empty.
//   - Type == "azd"   => RepoURL is set, ManifestURL is empty.
//
// Consumers should switch on Type rather than testing both URL fields for
// non-emptiness, so that adding future template types stays a single
// additive change instead of a chain of fallback branches. The invariant is
// asserted by TestMapAgentTemplateToDTO_ManifestUrlAndRepoUrlAreMutuallyExclusive.
type TemplateListItem struct {
	// Title is the human-readable template name from the upstream catalog.
	Title string `json:"title"`

	// Description is the upstream description string. May be empty.
	Description string `json:"description,omitempty"`

	// Languages lists language tokens (e.g., "python", "dotnetCsharp") the
	// template supports. Tokens match the values used internally by the
	// template picker.
	Languages []string `json:"languages"`

	// Type is the effective template type: "agent" for entries whose source
	// points directly at an agent.yaml manifest, or "azd" for entries whose
	// source is a full azd template repository.
	Type string `json:"type"`

	// ManifestURL is set when Type == "agent". This URL can be passed to
	// `azd ai agent init -m <url>` for a one-shot headless init.
	ManifestURL string `json:"manifestUrl,omitempty"`

	// RepoURL is set when Type == "azd". This URL/slug can be passed to
	// `azd init -t <url>` to scaffold the full project; the agent definition
	// inside the scaffolded project then drives `azd ai agent init`.
	RepoURL string `json:"repoUrl,omitempty"`

	// Tags is the raw extensionTags array from the upstream catalog.
	Tags []string `json:"tags,omitempty"`

	// Featured reports whether the template is tagged "featured" (curated
	// starter list).
	Featured bool `json:"featured"`

	// Recommended reports whether the template is tagged "recommended"
	// (default pre-selected template in interactive mode).
	Recommended bool `json:"recommended"`

	// InitCommand is the recommended next command to run. For Type == "agent"
	// it is `azd ai agent init -m <ManifestURL>`. For Type == "azd" it is
	// `azd init -t <RepoURL>` -- note that the agent extension must be run
	// separately after the core init completes.
	//
	// The URL segment is quoted with Go's %q verb so whitespace, embedded
	// double quotes, and similar tokenizer-breaking characters survive a
	// copy/paste into a POSIX or PowerShell prompt. The catalog is trusted
	// (curated upstream), so this is not a hardening boundary against
	// untrusted input; %q does NOT neutralize shell expansion of `$` or
	// backticks. Coding agents that prefer pre-tokenized argv should reach
	// for ManifestURL/RepoURL directly and skip InitCommand.
	InitCommand string `json:"initCommand"`
}

// sampleListResponse is the top-level JSON envelope. Wrapping the list lets us
// add metadata fields later without breaking consumers.
//
// The JSON field is intentionally named `templates` (not `samples`): the
// catalog source itself is "templates" in the upstream registry, and the
// `sample` namespace is purely the CLI surface label. Keeping the wire field
// stable means JSON consumers do not need to track CLI renames.
type sampleListResponse struct {
	Templates []TemplateListItem `json:"templates"`
}

// Known language tokens, kept in one place so error messages stay accurate.
var knownSampleListLanguages = []string{"python", "dotnetCsharp"}

// Known template type filter values.
var knownSampleListTypes = []string{TemplateTypeAgent, TemplateTypeAzd}

// SampleListAction owns the catalog-fetch + render side of `sample list`.
//
// Mirrors the local action convention used elsewhere in this extension
// (MonitorAction, ProjectShowAction): a small constructor in RunE builds
// the action with validated flags, then calls Run(ctx) which does the I/O
// and rendering. This separates command wiring (cobra plumbing) from
// behavior (catalog fetch + output) so the behavior is unit-testable
// without spinning up cobra.
type SampleListAction struct {
	flags *sampleListFlags
	// out is the stdout writer for the action. RunE injects cmd.OutOrStdout()
	// so tests can capture output without process-level redirection.
	out io.Writer
}

func newSampleListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &sampleListFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available agent samples that can be used with `azd ai agent init -m`.",
		Long: `List available agent samples from the curated catalog.

Each entry includes the manifest URL or repo URL that can be passed back into
` + "`azd ai agent init -m <url>`" + ` (for agent manifests) or ` + "`azd init -t <url>`" + `
(for full azd template repositories), and a ready-to-execute ` + "`initCommand`" + `
string so coding agents don't have to compose flags.

The catalog is fetched from the same source the interactive template picker uses.`,
		Example: `  # List all samples in the default text format
  azd ai agent sample list

  # List as JSON for programmatic consumption
  azd ai agent sample list --output json

  # Only Python samples
  azd ai agent sample list --language python

  # Only featured (curated) samples as JSON
  azd ai agent sample list --featured-only --output json

  # Only agent-manifest samples (ready for -m)
  azd ai agent sample list --type agent`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.output = extCtx.OutputFormat

			if err := validateSampleListFlags(flags); err != nil {
				return err
			}

			action := &SampleListAction{
				flags: flags,
				out:   cmd.OutOrStdout(),
			}

			return action.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&flags.language, "language", "",
		fmt.Sprintf("Filter by language token. Supported values: %s.", strings.Join(knownSampleListLanguages, ", ")))
	cmd.Flags().BoolVar(&flags.featuredOnly, "featured-only", false,
		"Only include samples tagged 'featured' (the curated starter list).")
	cmd.Flags().StringVar(&flags.templateType, "type", "",
		fmt.Sprintf("Filter by template type. Supported values: %s.", strings.Join(knownSampleListTypes, ", ")))

	// Default human format is "text": a paragraph-style list with title,
	// description, and manifest URL per entry. Wide tables collapse poorly
	// because catalog titles and URLs both routinely exceed 80 columns.
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "text"},
		Default:       "text",
	})

	return cmd
}

// Run fetches the catalog, applies filters, and renders the result in the
// selected output format.
func (a *SampleListAction) Run(ctx context.Context) error {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	templates, err := fetchAgentTemplates(ctx, httpClient)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("failed to fetch agent templates catalog from %s: %s", agentTemplatesURL, err),
			"check network connectivity and retry",
		)
	}

	items := buildTemplateListItems(templates, a.flags)

	switch normalizeOutputFormat(a.flags.output) {
	case "json":
		return printSampleListJSON(a.out, items)
	default:
		return printSampleListText(a.out, items)
	}
}

// validateSampleListFlags rejects unknown filter values before any network I/O.
func validateSampleListFlags(flags *sampleListFlags) error {
	if flags.language != "" && !slices.Contains(knownSampleListLanguages, flags.language) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unknown language %q", flags.language),
			fmt.Sprintf("use one of: %s", strings.Join(knownSampleListLanguages, ", ")),
		)
	}
	if flags.templateType != "" && !slices.Contains(knownSampleListTypes, flags.templateType) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unknown template type %q", flags.templateType),
			fmt.Sprintf("use one of: %s", strings.Join(knownSampleListTypes, ", ")),
		)
	}
	return nil
}

// buildTemplateListItems converts AgentTemplate entries into the public DTO,
// applying any filters from flags. The result is sorted: featured first, then
// alphabetically by title within each group.
func buildTemplateListItems(templates []AgentTemplate, flags *sampleListFlags) []TemplateListItem {
	filtered := make([]AgentTemplate, 0, len(templates))
	for _, t := range templates {
		if flags.language != "" && !slices.Contains(t.Languages, flags.language) {
			continue
		}
		if flags.featuredOnly && !t.isFeatured() {
			continue
		}
		if flags.templateType != "" && t.EffectiveType() != flags.templateType {
			continue
		}
		filtered = append(filtered, t)
	}

	// Stable ordering: featured first, alphabetical by title within each group.
	// Mirrors the order the interactive picker uses so the JSON output and the
	// curated UI present the same templates in the same order.
	featured, rest := partitionFeatured(filtered)
	ordered := append(featured, rest...)

	items := make([]TemplateListItem, 0, len(ordered))
	for _, t := range ordered {
		items = append(items, mapAgentTemplateToDTO(t))
	}
	return items
}

// mapAgentTemplateToDTO produces the public JSON shape for one template.
//
// Splits Source into ManifestURL or RepoURL based on EffectiveType so consumers
// know which init invocation to use without re-parsing the URL themselves.
func mapAgentTemplateToDTO(t AgentTemplate) TemplateListItem {
	effective := t.EffectiveType()
	item := TemplateListItem{
		Title:       t.Title,
		Description: t.Description,
		Languages:   append([]string(nil), t.Languages...),
		Type:        effective,
		Tags:        append([]string(nil), t.ExtensionTags...),
		Featured:    t.isFeatured(),
		Recommended: t.isRecommended(),
	}

	switch effective {
	case TemplateTypeAgent:
		item.ManifestURL = t.Source
		item.InitCommand = fmt.Sprintf("azd ai agent init -m %q", t.Source)
	case TemplateTypeAzd:
		item.RepoURL = t.Source
		item.InitCommand = fmt.Sprintf("azd init -t %q", t.Source)
	}

	return item
}

// normalizeOutputFormat collapses the SDK default placeholder to the human
// format so callers can switch on a finite set of values. The flag set's
// AllowedValues constrains `--output` to "json" or "text" at parse time,
// so anything else reaching this function is either the SDK's pre-parse
// sentinel ("default") or a programmatic caller.
func normalizeOutputFormat(s string) string {
	switch strings.ToLower(s) {
	case "json":
		return "json"
	default:
		return "text"
	}
}

func printSampleListJSON(w io.Writer, items []TemplateListItem) error {
	resp := sampleListResponse{Templates: items}
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling samples to JSON: %w", err)
	}
	// Write bytes directly to avoid an extra string allocation for what
	// can be a large catalog payload.
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

// printSampleListText emits each template as a three-line paragraph:
//
//	Sample: <title>
//	Description: <description>
//	Manifest: <manifestUrl or repoUrl>
//
// followed by a blank line. Designed to stay readable when titles and URLs
// each routinely exceed 80 columns, where a fixed-column table would wrap
// or truncate badly.
func printSampleListText(w io.Writer, items []TemplateListItem) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "No samples matched the supplied filters.")
		return err
	}

	for _, it := range items {
		url := it.ManifestURL
		if url == "" {
			url = it.RepoURL
		}
		if _, err := fmt.Fprintf(w, "Sample: %s\n", it.Title); err != nil {
			return err
		}
		if it.Description != "" {
			if _, err := fmt.Fprintf(w, "Description: %s\n", it.Description); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "Manifest: %s\n", url); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintln(w,
		"Run `azd ai agent sample list --output json` for the machine-readable form (includes ready-to-run initCommand).")
	return err
}
