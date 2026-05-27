// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// doc_catalog.go is the structured data layer behind `azd ai doc` and
// every `azd ai doc <category>` index command. Topic descriptions live
// in YAML front-matter at the top of each shipped `.md` file rather
// than in Go literals so authors can update content + metadata in one
// place. The loader runs at package init; a malformed shipped topic
// (missing closing fence, malformed YAML, unknown field) panics the
// extension on startup so the developer catches it before merge.
//
// Add a new doc category by:
//
//  1. Creating internal/cmd/skills/<name>/ with markdown topics that
//     include front-matter (short:, order:, optional references:).
//  2. Appending a DocCategory entry to docCategories with Name,
//     DisplayName, Short, Preamble, Examples.
//  3. Wiring a new cobra subcommand in root.go that mirrors the
//     existing newAgentCommand pattern.

package cmd

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DocCategory describes one sibling-extension topic group (e.g. agent).
// Topics is populated at package init from front-matter on the shipped
// markdown files; Examples is hardcoded here because it is per-category
// guidance the catalog renderer surfaces under "Examples:".
type DocCategory struct {
	Name        string
	DisplayName string
	Short       string
	Preamble    []string
	Topics      []DocTopic
	Examples    map[string]string
}

// DocTopic is one rendered row under "Available Commands:" in the
// category index. Name is the leaf verb a user runs (e.g.
// `azd ai doc agent configure`). Order is resolved from the *int
// front-matter field; absent values become 1000 so unordered topics
// sort after ordered ones.
type DocTopic struct {
	Name       string
	Short      string
	Order      int
	References []DocReference
}

// DocReference is a sub-doc pointer rendered under
// "References for `<topic>`:" when a topic ships nested guidance.
// Today no topic ships references; the scaffolding stays in place
// for future expansion and is exercised by synthetic-data tests.
type DocReference struct {
	Name  string `yaml:"name"`
	Short string `yaml:"short"`
}

// frontMatter is the on-disk YAML shape. Order is *int so the loader
// can distinguish "absent" from "explicitly 0" (the latter is allowed
// and sorts first). Unknown fields fail at parse time via
// yaml.Decoder.KnownFields(true) so typos like `ordr:` cannot silently
// corrupt the catalog order.
type frontMatter struct {
	Short      string         `yaml:"short"`
	Order      *int           `yaml:"order"`
	References []DocReference `yaml:"references"`
}

// orderFallback is the Order value assigned to topics whose front-matter
// has no `order:` field. It is intentionally larger than any reasonable
// hand-authored value so unordered topics sort last (then alphabetical).
const orderFallback = 1000

// utf8BOM is stripped from the start of any topic file before parsing.
// Some editors prepend it on save and yaml.Unmarshal will refuse to
// parse a value that begins with the BOM.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// docCategories lists every doc category whose markdown is embedded
// in this extension. Add a new entry (and a matching cobra subcommand
// in root.go) when adding topics for a new ai.* extension.
//
// Topics fields are POPULATED at package init by populateCatalog(); the
// var literal here just defines name/display/preamble/examples.
var docCategories = []DocCategory{
	{
		Name:        "agent",
		DisplayName: "Foundry agents (azure.ai.agents)",
		// Short is the one-liner shown next to the category name in
		// `azd ai doc`'s Available Documentation block. Include the
		// extension reference inline (parenthetically) so the
		// renderer can drop a separate DisplayName line without
		// losing the extension provenance.
		Short: "Create, configure, operate, and investigate Foundry agents " +
			"(azure.ai.agents).",
		Preamble: []string{
			"Each topic below is a self-contained contract you can read directly to drive the matching " +
				"`azd ai agent` commands.",
			"Use `azd ai doc agent <topic>` to print one topic's body in full.",
		},
		Examples: map[string]string{
			// Title prefixes chosen so the lexical sort applied by
			// helpformat.Examples yields a readable sequence:
			// "List ..." (L) sorts before "Print ..." (P).
			"List topics for the agents extension.": "azd ai doc agent",
			"Print the samples topic.":              "azd ai doc agent samples",
			"Print the initialize topic.":           "azd ai doc agent initialize",
			"Print the develop topic.":              "azd ai doc agent develop",
			"Print the configure topic.":            "azd ai doc agent configure",
			"Print the extend topic.":               "azd ai doc agent extend",
			"Print the deploy topic.":               "azd ai doc agent deploy",
			"Print the evaluate topic.":             "azd ai doc agent evaluate",
			"Print the operate topic.":              "azd ai doc agent operate",
			"Print the investigate topic.":          "azd ai doc agent investigate",
		},
	},
	{
		Name:        "connection",
		DisplayName: "Foundry project connections",
		// Connections are referenced from `azure.yaml` and (today)
		// managed by commands under `azd ai agent connection`. The
		// imperative CLI is expected to move to `azd ai connection`
		// once the namespace change in azure.ai.connections lands;
		// this category names the conceptual area so the docs do not
		// need to move when the command surface does.
		Short: "Add, configure, and manage Foundry project connections " +
			"(MCP, Azure AI Search, Bing, OpenAPI, OAuth2, ...).",
		Preamble: []string{
			"Connections are the credential + endpoint records that custom tools (MCP, OpenAPI, A2A) " +
				"and connection-bound built-in tools (azure_ai_search, bing_grounding) reference at runtime.",
			"Use `azd ai doc connection <topic>` to print one topic's body in full. " +
				"Start with `overview` for the mental model, then `add` for end-to-end recipes.",
		},
		Examples: map[string]string{
			"List topics for connections.":            "azd ai doc connection",
			"Print the overview topic.":               "azd ai doc connection overview",
			"Print the add topic (scenario recipes).": "azd ai doc connection add",
			"Print the categories reference.":         "azd ai doc connection categories",
			"Print the auth-types reference.":         "azd ai doc connection auth-types",
			"Print the imperative CLI reference.":     "azd ai doc connection manage",
		},
	},
	{
		Name:        "toolbox",
		DisplayName: "Foundry toolboxes",
		// Toolboxes are managed via the azd ai toolbox CLI today (from
		// the azure.ai.toolboxes extension). The azure.yaml
		// services.<name>.config.toolboxes[] block records the
		// declarative shape but the CLI is what materializes a toolbox
		// on Foundry today.
		Short: "Bundle connection-backed tools (MCP, Azure AI Search, A2A, Bing Custom Search) into a single MCP endpoint.",
		Preamble: []string{
			"A toolbox is a curated bundle of connection-backed tools that Foundry exposes as " +
				"a single MCP-compatible endpoint. Managed via the `azd ai toolbox` CLI (from " +
				"the `azure.ai.toolboxes` extension).",
			"Use `azd ai doc toolbox <topic>` to print one topic's body in full. " +
				"Start with `overview` for the lifecycle, then `add` for end-to-end recipes.",
		},
		Examples: map[string]string{
			"List topics for toolboxes.":              "azd ai doc toolbox",
			"Print the overview topic.":               "azd ai doc toolbox overview",
			"Print the add topic (scenario recipes).": "azd ai doc toolbox add",
			"Print the tool-types reference.":         "azd ai doc toolbox tools",
			"Print the consumer-side runtime guide.":  "azd ai doc toolbox consume",
		},
	},
	{
		Name:        "skill",
		DisplayName: "Foundry skills (azure.ai.skills)",
		// Foundry skills are centrally-stored, versioned behavioral
		// guidelines a Hosted agent downloads and injects as
		// instructions. Managed via the `azd ai skill` CLI (from the
		// `azure.ai.skills` extension). Intentionally distinct from
		// the embedded `azd-ai-skill` pack installed by
		// `azd ai doc install skill` -- that is a coding-agent pack
		// consumed by tools like Claude Code / GitHub Copilot.
		Short: "Manage Foundry skills -- versioned, project-scoped behavioral guidelines a Hosted agent downloads and injects " +
			"(azure.ai.skills).",
		Preamble: []string{
			"Foundry skills are reusable behavioral guidelines stored centrally on a Foundry project. " +
				"A Hosted agent downloads them at build time and the agent runtime injects them as " +
				"additional instructions on every session.",
			"Use `azd ai doc skill <topic>` to print one topic's body in full. " +
				"Start with `overview` for the mental model, then `manage` for the CLI.",
		},
		Examples: map[string]string{
			"List topics for skills.":                   "azd ai doc skill",
			"Print the overview topic.":                 "azd ai doc skill overview",
			"Print the management CLI reference.":       "azd ai doc skill manage",
			"Print the cross-project sharing recipes.":  "azd ai doc skill share",
			"Print the hosted-agent consumption guide.": "azd ai doc skill consume",
		},
	},
	{
		Name:        "routine",
		DisplayName: "Foundry routines (azure.ai.routines)",
		// Foundry routines pair a trigger (timer / recurring / event)
		// with an action (invoke an agent). Managed via the
		// `azd ai routine` CLI (from the `azure.ai.routines`
		// extension). This is how a deployed agent gets billed work
		// that fires on its own (scheduled or event-driven), as
		// opposed to the on-demand `azd ai agent invoke` path.
		Short: "Manage Foundry routines -- trigger + action pairs that fire on a schedule or event and invoke an agent " +
			"(azure.ai.routines).",
		Preamble: []string{
			"A routine pairs a trigger (timer, recurring schedule, or external event) with an action " +
				"(invoke an agent). Foundry fires the routine on its own when the trigger matches, " +
				"or you can fire it manually with `azd ai routine dispatch`. Each firing records a " +
				"RoutineRun row visible via `routine run list`.",
			"Use `azd ai doc routine <topic>` to print one topic's body in full. " +
				"Start with `overview` for the mental model, then `manage` for the CLI.",
		},
		Examples: map[string]string{
			"List topics for routines.":               "azd ai doc routine",
			"Print the overview topic.":               "azd ai doc routine overview",
			"Print the trigger-types reference.":      "azd ai doc routine triggers",
			"Print the action-types reference.":       "azd ai doc routine actions",
			"Print the management CLI reference.":     "azd ai doc routine manage",
			"Print the dispatch + run-history guide.": "azd ai doc routine dispatch",
		},
	},
}

// init populates the Topics field of every DocCategory from the
// embedded markdown files. A malformed front-matter block (missing
// closing fence, unknown field, malformed YAML) is treated as a
// development bug that shipped to the binary and panics here so a CI
// load picks it up before any user runs the command.
//
// init only depends on the //go:embed FS in doc_agent.go which is
// available at package-init time.
func init() {
	for i := range docCategories {
		topics, err := loadCategoryTopics(docCategories[i].Name)
		if err != nil {
			panic(fmt.Errorf("doc catalog: loading %q: %w", docCategories[i].Name, err))
		}
		docCategories[i].Topics = topics
	}
}

// loadCategoryTopics walks skills/<category>/*.md, parses each file's
// front-matter, and returns the topic list sorted by Order asc then
// Name asc. Returns an error wrapping the file path on any per-file
// failure so the package-init panic message is actionable.
func loadCategoryTopics(category string) ([]DocTopic, error) {
	dir := categoryDir(category)
	entries, err := fs.ReadDir(skillsFS, dir)
	if err != nil {
		return nil, fmt.Errorf("read embedded skills dir %q: %w", dir, err)
	}
	var topics []DocTopic
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".md")
		if stem == e.Name() {
			continue // non-markdown file, skip
		}
		raw, err := fs.ReadFile(skillsFS, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", path.Join(dir, e.Name()), err)
		}
		fm, _, err := parseFrontMatter(raw)
		if err != nil {
			return nil, fmt.Errorf("parse front-matter in %q: %w", path.Join(dir, e.Name()), err)
		}
		order := orderFallback
		if fm.Order != nil {
			order = *fm.Order
		}
		topics = append(topics, DocTopic{
			Name:       stem,
			Short:      fm.Short,
			Order:      order,
			References: fm.References,
		})
	}
	sort.SliceStable(topics, func(i, j int) bool {
		if topics[i].Order != topics[j].Order {
			return topics[i].Order < topics[j].Order
		}
		return topics[i].Name < topics[j].Name
	})
	return topics, nil
}

// parseFrontMatter extracts the YAML front-matter block (if any) from
// raw and returns the parsed struct plus the byte offset of the first
// body byte after the closing fence's trailing newline. When no
// opening fence is present at the start of the file the returned
// frontMatter is zero and the offset is 0 (caller treats the entire
// file as body).
//
// Failure modes returned as errors:
//   - opening fence present but no closing fence
//   - malformed YAML in the block
//   - unknown field in the block (via yaml.Decoder.KnownFields(true))
//
// UTF-8 BOM at byte 0 is stripped before fence detection so editors
// that save with BOM don't break the parse.
func parseFrontMatter(raw []byte) (frontMatter, int, error) {
	body := raw
	bomOffset := 0
	if bytes.HasPrefix(body, utf8BOM) {
		body = body[len(utf8BOM):]
		bomOffset = len(utf8BOM)
	}
	openLen := openingFenceLen(body)
	if openLen == 0 {
		// No front-matter at all. The caller will treat the entire
		// file as body. Offset 0 means "do not strip anything".
		return frontMatter{}, 0, nil
	}
	rest := body[openLen:]
	closeStart, closeLen, ok := findClosingFence(rest)
	if !ok {
		return frontMatter{}, 0, fmt.Errorf("opening `---` fence has no matching closing fence")
	}
	yamlBlock := rest[:closeStart]
	dec := yaml.NewDecoder(bytes.NewReader(yamlBlock))
	dec.KnownFields(true)
	var fm frontMatter
	if err := dec.Decode(&fm); err != nil {
		return frontMatter{}, 0, fmt.Errorf("decode YAML: %w", err)
	}
	// Body starts after the closing fence + its trailing newline.
	bodyStart := bomOffset + openLen + closeStart + closeLen
	return fm, bodyStart, nil
}

// openingFenceLen returns the byte length of the opening fence
// (`---\n` or `---\r\n`) when one is present at the start of body,
// or 0 when not. The fence MUST start at byte 0 -- leading whitespace
// or a shebang disables front-matter detection (matching Hugo, Jekyll,
// and most Markdown ecosystems).
func openingFenceLen(body []byte) int {
	if bytes.HasPrefix(body, []byte("---\r\n")) {
		return 5
	}
	if bytes.HasPrefix(body, []byte("---\n")) {
		return 4
	}
	return 0
}

// findClosingFence searches rest for the next `---` on its own line
// and returns (start, len, true) when found. start is the byte offset
// of the `-` characters from rest[0]; len includes the trailing
// newline (so the caller can compute body-start in one add). Returns
// (0, 0, false) when no closing fence exists.
//
// "Own line" means the previous byte is '\n' (or rest start) AND the
// `---` is followed by either EOL or a CRLF/LF.
func findClosingFence(rest []byte) (start, fenceLen int, ok bool) {
	// We scan line-by-line. A line consisting of exactly `---` (with
	// optional trailing CR) marks the closing fence.
	pos := 0
	for pos < len(rest) {
		// IndexByte returns an offset RELATIVE to rest[pos:], not
		// absolute. The line length is therefore `nl` bytes (not
		// `nl - pos`); the +1 accounts for the trailing newline so
		// the caller's body offset lands on the byte AFTER the LF.
		nl := bytes.IndexByte(rest[pos:], '\n')
		var line []byte
		if nl < 0 {
			line = rest[pos:]
		} else {
			line = rest[pos : pos+nl]
		}
		stripped := bytes.TrimRight(line, "\r")
		if bytes.Equal(stripped, []byte("---")) {
			if nl < 0 {
				// Closing fence at EOF without a trailing newline.
				return pos, len(line), true
			}
			return pos, nl + 1, true
		}
		if nl < 0 {
			break
		}
		pos += nl + 1
	}
	return 0, 0, false
}

// stripFrontMatter removes the leading front-matter block from a
// topic body (or returns raw unchanged when none is present). The
// returned bytes are byte-identical to the source from the byte AFTER
// the closing fence's trailing newline through EOF. A regression test
// pins this so a refactor cannot accidentally munge whitespace.
//
// Errors here are deliberately swallowed: parseFrontMatter is the
// authoritative loader and panics at package init on any defect, so
// by the time this runs the file is known-good. Belt-and-suspenders:
// if a defect somehow slipped through, return the raw bytes so the
// user still sees SOMETHING rather than an empty topic body.
func stripFrontMatter(raw []byte) []byte {
	_, bodyStart, err := parseFrontMatter(raw)
	if err != nil || bodyStart == 0 {
		// No front-matter, or parse failure -- return the source
		// (post-BOM strip) so the user sees the markdown body either
		// way.
		if bytes.HasPrefix(raw, utf8BOM) {
			return raw[len(utf8BOM):]
		}
		return raw
	}
	return raw[bodyStart:]
}

// FindCategory returns the DocCategory matching name (e.g. "agent"),
// or nil when no such category is registered. Exported so the cobra
// command constructors in root.go can fetch the catalog row that
// drives their --help Description and Footer.
func FindCategory(name string) *DocCategory {
	for i := range docCategories {
		if docCategories[i].Name == name {
			return &docCategories[i]
		}
	}
	return nil
}
