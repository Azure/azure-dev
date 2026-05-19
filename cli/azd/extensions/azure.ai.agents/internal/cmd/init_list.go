// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// initListFlags holds the optional filters for `azd ai agent init list`.
type initListFlags struct {
	language     string
	featuredOnly bool
	templateType string
	output       string
}

// TemplateListItem is the public JSON contract for a single template emitted by
// `azd ai agent init list -o json`.
//
// Consumers (especially AI coding agents) read this to discover which manifest
// URLs and repo URLs they can pass to `azd ai agent init -m <url>` or
// `azd init -t <url>` without scraping documentation or guessing slugs.
//
// Schema stability: fields added in future versions MUST be additive; existing
// fields keep their semantics. Consumers must ignore unknown fields.
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
	// `azd init -t <RepoURL>` — note that the agent extension must be run
	// separately after the core init completes.
	InitCommand string `json:"initCommand"`
}

// initListResponse is the top-level JSON envelope. Wrapping the list lets us
// add metadata fields later without breaking consumers.
type initListResponse struct {
	Templates []TemplateListItem `json:"templates"`
}

// Known language tokens, kept in one place so error messages stay accurate.
var knownInitListLanguages = []string{"python", "dotnetCsharp"}

// Known template type filter values.
var knownInitListTypes = []string{TemplateTypeAgent, TemplateTypeAzd}

func newInitListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &initListFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available agent templates that can be used with `azd ai agent init -m`.",
		Long: `List available agent templates from the curated catalog.

Each entry includes the manifest URL or repo URL that can be passed back into
` + "`azd ai agent init -m <url>`" + ` (for agent manifests) or ` + "`azd init -t <url>`" + `
(for full azd template repositories), and a ready-to-execute ` + "`initCommand`" + `
string so coding agents don't have to compose flags.

The catalog is fetched from the same source the interactive template picker uses.`,
		Example: `  # List all templates as a table
  azd ai agent init list

  # List as JSON for programmatic consumption
  azd ai agent init list --output json

  # Only Python templates
  azd ai agent init list --language python

  # Only featured (curated) templates as JSON
  azd ai agent init list --featured-only --output json

  # Only agent-manifest templates (ready for -m)
  azd ai agent init list --type agent`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.output = extCtx.OutputFormat

			if err := validateInitListFlags(flags); err != nil {
				return err
			}

			ctx := cmd.Context()

			httpClient := &http.Client{
				Timeout: 30 * time.Second,
			}

			templates, err := fetchAgentTemplates(ctx, httpClient)
			if err != nil {
				return exterrors.Dependency(
					exterrors.CodeGitHubDownloadFailed,
					fmt.Sprintf("failed to fetch agent templates catalog: %s", err),
					"check network connectivity and retry; the catalog is fetched from "+agentTemplatesURL,
				)
			}

			items := buildTemplateListItems(templates, flags)

			switch normalizeOutputFormat(flags.output) {
			case "json":
				return printInitListJSON(items)
			default:
				return printInitListTable(items)
			}
		},
	}

	cmd.Flags().StringVar(&flags.language, "language", "",
		fmt.Sprintf("Filter by language token. Supported values: %s.", strings.Join(knownInitListLanguages, ", ")))
	cmd.Flags().BoolVar(&flags.featuredOnly, "featured-only", false,
		"Only include templates tagged 'featured' (the curated starter list).")
	cmd.Flags().StringVar(&flags.templateType, "type", "",
		fmt.Sprintf("Filter by template type. Supported values: %s.", strings.Join(knownInitListTypes, ", ")))

	// Match the rest of the extension: only "table" and "json" are valid here,
	// and "table" is the default for human consumption. The SDK substitutes
	// the default when --output is not passed explicitly.
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "table",
	})

	return cmd
}

// validateInitListFlags rejects unknown filter values before any network I/O.
func validateInitListFlags(flags *initListFlags) error {
	if flags.language != "" && !slices.Contains(knownInitListLanguages, flags.language) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unknown language %q", flags.language),
			fmt.Sprintf("use one of: %s", strings.Join(knownInitListLanguages, ", ")),
		)
	}
	if flags.templateType != "" && !slices.Contains(knownInitListTypes, flags.templateType) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unknown template type %q", flags.templateType),
			fmt.Sprintf("use one of: %s", strings.Join(knownInitListTypes, ", ")),
		)
	}
	return nil
}

// buildTemplateListItems converts AgentTemplate entries into the public DTO,
// applying any filters from flags. The result is sorted: featured first, then
// alphabetically by title within each group.
func buildTemplateListItems(templates []AgentTemplate, flags *initListFlags) []TemplateListItem {
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
		item.InitCommand = fmt.Sprintf("azd ai agent init -m %s", t.Source)
	case TemplateTypeAzd:
		item.RepoURL = t.Source
		item.InitCommand = fmt.Sprintf("azd init -t %s", t.Source)
	}

	return item
}

// normalizeOutputFormat collapses the SDK default placeholder to the human
// format so callers can switch on a finite set of values.
func normalizeOutputFormat(s string) string {
	switch strings.ToLower(s) {
	case "json":
		return "json"
	default:
		// The SDK uses "default" as its sentinel before substitution; treat it
		// (and any unrecognized value the validator already accepted) as table.
		return "table"
	}
}

func printInitListJSON(items []TemplateListItem) error {
	resp := initListResponse{Templates: items}
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling templates to JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func printInitListTable(items []TemplateListItem) error {
	if len(items) == 0 {
		fmt.Println("No templates matched the supplied filters.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TITLE\tLANG\tTYPE\tTAGS\tURL")
	fmt.Fprintln(w, "-----\t----\t----\t----\t---")

	for _, it := range items {
		url := it.ManifestURL
		if url == "" {
			url = it.RepoURL
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			it.Title,
			strings.Join(it.Languages, ","),
			it.Type,
			summarizeTagsForTable(it),
			url,
		)
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing template list table: %w", err)
	}

	fmt.Println()
	fmt.Println("Run `azd ai agent init list --output json` for the machine-readable form (includes ready-to-run initCommand).")
	return nil
}

// summarizeTagsForTable surfaces only the tags that matter for headless
// consumers (featured, recommended) to keep the column narrow.
func summarizeTagsForTable(it TemplateListItem) string {
	tags := make([]string, 0, 2)
	if it.Featured {
		tags = append(tags, "featured")
	}
	if it.Recommended {
		tags = append(tags, "recommended")
	}
	if len(tags) == 0 {
		return "-"
	}
	return strings.Join(tags, ",")
}
