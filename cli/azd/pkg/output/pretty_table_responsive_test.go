// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/require"
)

// responsiveInput is the row type used by the responsive-layout tests.
type responsiveInput struct {
	Id               string
	Name             string
	Status           string
	InstalledVersion string
	LatestVersion    string
	Category         string
	Priority         string
	Source           string
	UpdateAvailable  bool
}

// toolCheckTestColumns mirrors the azd tool check column configuration:
// wrapping NAME card title, truncatable STATUS/LATEST, card value omission.
func toolCheckTestColumns() []PrettyColumn {
	return []PrettyColumn{
		{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
		{
			Column:      Column{Heading: "NAME", ValueTemplate: "{{.Name}}"},
			Priority:    2,
			CardTitle:   true,
			Wrappable:   true,
			Truncatable: true,
		},
		{
			Column:      Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
			Priority:    1,
			Truncatable: true,
		},
		{
			Column: Column{
				Heading:       "INSTALLED",
				ValueTemplate: `{{if .InstalledVersion}}{{.InstalledVersion}}{{else}}-{{end}}`,
			},
			CardValueTemplate: `{{if .InstalledVersion}}{{.InstalledVersion}}{{end}}`,
			Priority:          1,
		},
		{
			Column:            Column{Heading: "LATEST", ValueTemplate: "{{.LatestVersion}}"},
			CardValueTemplate: `{{if or .UpdateAvailable (not .InstalledVersion)}}{{.LatestVersion}}{{end}}`,
			Priority:          3,
			Truncatable:       true,
		},
	}
}

func TestTruncateWithEllipsis(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"fits", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncates", "0.1.20-preview", 9, "0.1.20-p…"},
		{"keeps min five chars", "abcdefghij", 6, "abcde…"},
		{"wide runes", "日本語テスト", 7, "日本語…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateWithEllipsis(tt.input, tt.width)
			require.Equal(t, tt.want, got)
			ceiling := max(tt.width, minVisibleChars+displayWidth(ellipsis))
			require.LessOrEqual(t, displayWidth(stripTerminalEscapes(got)), ceiling)
		})
	}
}

func TestWrapValue(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		width     int
		maxLines  int
		wantLines []string
		wantTrunc bool
	}{
		{
			name:      "two lines no truncation",
			input:     "GitHub Copilot Chat VS Code Extension",
			width:     20,
			maxLines:  2,
			wantLines: []string{"GitHub Copilot Chat", "VS Code Extension"},
			wantTrunc: false,
		},
		{
			name:      "truncates beyond max lines",
			input:     "one two three four five six seven eight",
			width:     10,
			maxLines:  2,
			wantTrunc: true,
		},
		{
			name:      "single word longer than width",
			input:     "supercalifragilistic",
			width:     8,
			maxLines:  2,
			wantTrunc: true,
		},
		{
			// Regression: multiple short words at a narrow width must still cap at
			// maxLines (this previously produced 4 lines for "Bicep VS Code
			// Extension" because a carried line plus a hard-split next word blew
			// past the cap).
			name:      "many short words capped at two lines",
			input:     "Bicep VS Code Extension",
			width:     6,
			maxLines:  2,
			wantLines: []string{"Bicep", "VS …"},
			wantTrunc: true,
		},
		{
			name:      "word wider than narrow column still caps",
			input:     "GitHub Copilot Chat VS Code Extension",
			width:     6,
			maxLines:  2,
			wantTrunc: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines, trunc := wrapValue(tt.input, tt.width, tt.maxLines)
			require.LessOrEqual(t, len(lines), tt.maxLines)
			for _, ln := range lines {
				require.LessOrEqual(t, displayWidth(ln), tt.width, "line %q exceeds width", ln)
			}
			require.Equal(t, tt.wantTrunc, trunc)
			if tt.wantLines != nil {
				require.Equal(t, tt.wantLines, lines)
			}
		})
	}
}

func TestLayoutCellPreservesEscapedValues(t *testing.T) {
	// A value containing escape sequences must not be truncated/wrapped.
	value := "\x1b[36mhttps://example.com/a-very-long-link\x1b[0m"
	lines, trunc := layoutCell(value, 10, false)
	require.False(t, trunc)
	require.Equal(t, []string{value}, lines)
}

func TestShrinkOrderLeastImportantFirst(t *testing.T) {
	cols, err := parseColumns([]PrettyColumn{
		{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
		{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, Priority: 2, Wrappable: true},
		{Column: Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"}, Priority: 1, Truncatable: true},
		{Column: Column{Heading: "CATEGORY", ValueTemplate: "{{.Category}}"}, Priority: 3, Truncatable: true},
		{Column: Column{Heading: "PRIORITY", ValueTemplate: "{{.Priority}}"}, Priority: 3, Truncatable: true},
	})
	require.NoError(t, err)

	order := shrinkOrder(cols)
	// Indices: 1=NAME(p2), 2=STATUS(p1), 3=CATEGORY(p3), 4=PRIORITY(p3).
	// Highest priority first; equal priority right-most first → PRIORITY, CATEGORY, NAME, STATUS.
	require.Equal(t, []int{4, 3, 1, 2}, order)
}

func TestFitColumnsTruncatesLeastImportantFirst(t *testing.T) {
	cols, err := parseColumns(toolCheckTestColumns())
	require.NoError(t, err)
	// Natural widths for ID, NAME, STATUS, INSTALLED, LATEST.
	natural := []int{19, 37, 18, 14, 14}

	widths := fitColumns(cols, natural, 100)
	require.LessOrEqual(t, rowWidth(widths), 100)
	// ID is not shrinkable and keeps its natural width.
	require.Equal(t, 19, widths[0])
	// LATEST (least important truncatable) shrinks before STATUS (priority 1).
	require.Less(t, widths[4], natural[4])
	require.Equal(t, natural[2], widths[2], "STATUS should not shrink while LATEST can absorb the deficit")
}

func TestBuildColumnHint(t *testing.T) {
	previousNoColor := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previousNoColor })

	require.Equal(t, "", buildColumnHint(5, 5, false))
	require.Equal(t,
		"Resize the terminal or run with -o json for full details.",
		buildColumnHint(5, 5, true))
	require.Equal(t,
		"Showing 4 of 6 columns. Resize the terminal or run with -o json for full details.",
		buildColumnHint(4, 6, false))
}

func TestResponsiveFullTableTruncatesAndHints(t *testing.T) {
	rows := []responsiveInput{
		{
			Id: "GitHub.copilot-chat", Name: "GitHub Copilot Chat VS Code Extension",
			Status: "Not installed", LatestVersion: "1.0.6-preview-build",
		},
	}
	// Width 100 selects the full breakpoint, where overflow truncates values
	// (no columns are dropped) and the hint omits the column count.
	formatter := &PrettyTableFormatter{ConsoleWidthFn: func() int { return 100 }}
	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns:              toolCheckTestColumns(),
		ResponsiveColumnHint: true,
	})
	require.NoError(t, err)
	out := stripTerminalEscapes(buf.String())

	// All columns remain visible at the full breakpoint.
	require.Contains(t, out, "LATEST")
	// The hint appears without a column count (full breakpoint, values truncated).
	require.Contains(t, out, "Resize the terminal or run with -o json for full details.")
	require.NotContains(t, out, "Showing")
	// No row exceeds the terminal width.
	for line := range strings.SplitSeq(out, "\n") {
		require.LessOrEqual(t, displayWidth(line), 100, "line exceeds width: %q", line)
	}
}

func TestResponsiveCompactAddsHiddenColumnAndHint(t *testing.T) {
	rows := []responsiveInput{
		{Id: "az-cli", Name: "Azure CLI", Status: "Up to date", InstalledVersion: "1.0.0", LatestVersion: "1.0.0"},
	}
	formatter := &PrettyTableFormatter{ConsoleWidthFn: func() int { return 80 }}
	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns:              toolCheckTestColumns(),
		ResponsiveColumnHint: true,
	})
	require.NoError(t, err)
	out := stripTerminalEscapes(buf.String())

	// LATEST (priority 3) is hidden; the "⋯" placeholder column is present.
	require.NotContains(t, out, "LATEST")
	require.Contains(t, out, hiddenColumnHeading)
	require.Contains(t, out,
		"Showing 4 of 5 columns. Resize the terminal or run with -o json for full details.")
}

func TestResponsiveCompactNoHintWhenNoColumnsHidden(t *testing.T) {
	rows := []responsiveInput{{Id: "az-cli", Status: "ok"}}
	cols := []PrettyColumn{
		{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
		{Column: Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"}, Priority: 1},
	}
	formatter := &PrettyTableFormatter{ConsoleWidthFn: func() int { return 80 }}
	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns:              cols,
		ResponsiveColumnHint: true,
	})
	require.NoError(t, err)
	out := buf.String()
	require.NotContains(t, out, "Showing")
	require.NotContains(t, out, "Resize the terminal")
	require.NotContains(t, out, hiddenColumnHeading)
}

func TestResponsiveColumnHintDisabledByDefault(t *testing.T) {
	rows := []responsiveInput{{Id: "az-cli", Name: "Azure CLI", Status: "ok", LatestVersion: "1.0.0"}}
	formatter := &PrettyTableFormatter{ConsoleWidthFn: func() int { return 80 }}
	buf := &bytes.Buffer{}
	// ResponsiveColumnHint defaults to false → no "⋯" column or hint message.
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{Columns: toolCheckTestColumns()})
	require.NoError(t, err)
	out := buf.String()
	require.NotContains(t, out, "Showing")
	require.NotContains(t, out, "Resize the terminal")
}

func TestCardTitleGroupedLayout(t *testing.T) {
	previousNoColor := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = previousNoColor })

	rows := []responsiveInput{
		{
			Id: "az-cli", Name: "Azure CLI", Status: "Installed",
			InstalledVersion: "2.68.0", Category: "cli", Priority: "recommended",
		},
		{
			Id: "vscode-azure-tools", Name: "Azure Tools VS Code Extension", Status: "Not installed",
			Category: "vscode-extension", Priority: "recommended",
		},
	}
	columns := []PrettyColumn{
		{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
		{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, Priority: 2, CardTitle: true},
		{Column: Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"}, Priority: 1},
		{
			Column: Column{
				Heading:       "INSTALLED",
				ValueTemplate: `{{if .InstalledVersion}}{{.InstalledVersion}}{{else}}-{{end}}`,
			},
			CardValueTemplate: `{{if .InstalledVersion}}{{.InstalledVersion}}{{end}}`,
			Priority:          1,
		},
		{Column: Column{Heading: "CATEGORY", ValueTemplate: "{{.Category}}"}, Priority: 3},
		{Column: Column{Heading: "PRIORITY", ValueTemplate: "{{.Priority}}"}, Priority: 3},
	}
	formatter := &PrettyTableFormatter{ConsoleWidthFn: func() int { return 40 }}
	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns:         columns,
		CardGroupColumn: "CATEGORY",
	})
	require.NoError(t, err)
	out := buf.String()
	stripped := stripTerminalEscapes(out)

	// Group headers retain the labeled, gray divider style.
	require.Contains(t, stripped, "── CATEGORY: cli ")
	require.Contains(t, stripped, "── CATEGORY: vscode-extension ")
	// The NAME is rendered as a highlighted title (no "NAME:" label) and colored.
	require.NotContains(t, stripped, "NAME:")
	require.Regexp(t, sgrPrefixPattern("Azure CLI"), out)
	// CATEGORY is the group header, not a card field: any "CATEGORY:" occurrence
	// must be part of a "── CATEGORY:" divider line.
	for line := range strings.SplitSeq(stripped, "\n") {
		if strings.Contains(line, "CATEGORY:") {
			require.True(t, strings.HasPrefix(line, "── CATEGORY:"),
				"CATEGORY appeared as a card field: %q", line)
		}
	}
	// Card value omission: the not-installed tool omits INSTALLED.
	require.NotContains(t, stripped, "INSTALLED:  -")
	// No box-drawing borders in the titled card layout.
	require.NotContains(t, out, "┌")
}

func TestCardTitleUngroupedBorderless(t *testing.T) {
	rows := []responsiveInput{
		{Id: "az-cli", Name: "Azure CLI", Status: "Up to date", InstalledVersion: "1.0.0", LatestVersion: "1.0.0"},
	}
	formatter := &PrettyTableFormatter{ConsoleWidthFn: func() int { return 40 }}
	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{Columns: toolCheckTestColumns()})
	require.NoError(t, err)
	out := buf.String()
	// Ungrouped titled cards are borderless (no box-drawing characters).
	require.NotContains(t, out, "┌")
	require.NotContains(t, out, "│")
	require.Contains(t, out, "Azure CLI")
	require.Contains(t, out, "ID:")
	// LATEST equals INSTALLED for an up-to-date tool → omitted from the card.
	require.NotContains(t, stripTerminalEscapes(out), "LATEST:")
}

func TestAlignLeadingSymbols(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "no leading symbol leaves values untouched",
			in:   []string{"Up to date", "Not installed", "Installed"},
			want: []string{"Up to date", "Not installed", "Installed"},
		},
		{
			name: "pads plain values to align under symbol text",
			in:   []string{"⟳ Update available", "Up to date", "Not installed"},
			want: []string{"⟳ Update available", "  Up to date", "  Not installed"},
		},
		{
			name: "value that already has a symbol is unchanged",
			in:   []string{"⟳ Update available", "⚠ Incompatible"},
			want: []string{"⟳ Update available", "⚠ Incompatible"},
		},
		{
			name: "empty values are not padded",
			in:   []string{"⟳ Update available", ""},
			want: []string{"⟳ Update available", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, alignLeadingSymbols(tt.in))
		})
	}
}

func TestResponsiveStatusAlignment(t *testing.T) {
	// dataLineContaining returns the stripped rendered line containing substr.
	dataLineContaining := func(out, substr string) string {
		for line := range strings.SplitSeq(out, "\n") {
			if strings.Contains(line, substr) {
				return line
			}
		}
		return ""
	}
	textColumn := func(line, text string) int {
		before, _, ok := strings.Cut(line, text)
		require.True(t, ok, "text %q not found in %q", text, line)
		return displayWidth(before)
	}

	columns := []PrettyColumn{
		{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
		{
			Column:             Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
			Priority:           1,
			AlignLeadingSymbol: true,
		},
		{Column: Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"}, Priority: 1},
	}

	// When a symbol is present, the status text aligns: "Update available" and
	// "Up to date" begin at the same display column (the no-padding case is
	// covered directly by TestAlignLeadingSymbols).
	rows := []responsiveInput{
		{Id: "a", Status: "⟳ Update available", Source: "azd"},
		{Id: "b", Status: "Up to date", Source: "azd"},
	}
	formatter := &PrettyTableFormatter{ConsoleWidthFn: func() int { return 120 }}
	buf := &bytes.Buffer{}
	require.NoError(t, formatter.Format(rows, buf, PrettyTableFormatterOptions{Columns: columns}))
	out := stripTerminalEscapes(buf.String())

	updateCol := textColumn(dataLineContaining(out, "Update available"), "Update available")
	upToDateCol := textColumn(dataLineContaining(out, "Up to date"), "Up to date")
	require.Equal(t, updateCol, upToDateCol)
}

func TestEllipsisAndHiddenColumnGlyphs(t *testing.T) {
	// The package uses the single-character ellipsis for truncated values and
	// three middle dots for the hidden-column placeholder header.
	require.Equal(t, "…", ellipsis)
	require.Equal(t, "···", hiddenColumnHeading)
	require.Equal(t, 1, displayWidth(ellipsis))
	require.Equal(t, 3, displayWidth(hiddenColumnHeading))

	require.Equal(t, "0.1.2…", truncateWithEllipsis("0.1.20-preview", 6))
}

func TestColorLineLikeValue(t *testing.T) {
	previousNoColor := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = previousNoColor })

	// A ColorFunc that classifies by trimmed content (like extension status).
	warn := func(s string) string {
		if strings.TrimSpace(s) == "⟳ Update available" {
			return WithWarningFormat(s)
		}
		return WithGrayFormat(s)
	}

	// Non-truncated: the full value keeps its classified color.
	full := colorLineLikeValue(warn, "⟳ Update available", "⟳ Update available")
	require.Equal(t, WithWarningFormat("⟳ Update available"), full)

	// Truncated: classification stays based on the full value, but the visible
	// (truncated) text is what gets wrapped — so it stays warning-colored even
	// though the truncated text alone would classify as gray.
	truncatedLine := "⟳ Update availa…"
	got := colorLineLikeValue(warn, "⟳ Update available", truncatedLine)
	require.Contains(t, got, truncatedLine)
	require.NotEqual(t, WithGrayFormat(truncatedLine), got)
	// It carries the same leading SGR prefix as the warning-colored full value.
	warnPrefix := strings.SplitAfter(WithWarningFormat("x"), "m")[0]
	require.True(t, strings.HasPrefix(got, warnPrefix),
		"truncated line should keep the warning color prefix; got %q", got)
}

func TestResponsiveTruncatedStatusKeepsColor(t *testing.T) {
	previousNoColor := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = previousNoColor })

	statusColor := func(s string) string {
		switch strings.TrimSpace(s) {
		case "⟳ Update available":
			return WithWarningFormat(s)
		case "Up to date":
			return WithSuccessFormat(s)
		default:
			return WithGrayFormat(s)
		}
	}
	columns := []PrettyColumn{
		{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
		{
			Column:             Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
			Priority:           1,
			Truncatable:        true,
			AlignLeadingSymbol: true,
			ColorFunc:          statusColor,
		},
		{Column: Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"}, Priority: 1},
	}
	rows := []responsiveInput{
		{Id: "microsoft.azd.extensions.preview.longbuild", Status: "⟳ Update available", Source: "azd"},
	}
	// A narrow width (still in the compact table range) forces STATUS to truncate.
	formatter := &PrettyTableFormatter{ConsoleWidthFn: func() int { return 60 }}
	buf := &bytes.Buffer{}
	require.NoError(t, formatter.Format(rows, buf, PrettyTableFormatterOptions{Columns: columns}))
	out := buf.String()

	// The status value is truncated…
	require.Contains(t, stripTerminalEscapes(out), "…")
	// …and the truncated status still carries the warning color prefix.
	warnPrefix := strings.SplitAfter(WithWarningFormat("x"), "m")[0]
	require.Contains(t, out, warnPrefix+"⟳")
}

func TestColumnFloorWrappableTruncatable(t *testing.T) {
	cols, err := parseColumns([]PrettyColumn{
		{Column: Column{Heading: "WRAPONLY", ValueTemplate: "{{.Name}}"}, Wrappable: true},
		{Column: Column{Heading: "BOTH", ValueTemplate: "{{.Name}}"}, Wrappable: true, Truncatable: true},
		{Column: Column{Heading: "TRUNC", ValueTemplate: "{{.Name}}"}, Truncatable: true},
		{Column: Column{Heading: "FIXED", ValueTemplate: "{{.Name}}"}},
	})
	require.NoError(t, err)

	natural := 40
	truncFloor := minVisibleChars + displayWidth(ellipsis)
	// Wrap-only floors near natural/maxWrapLines so all content fits in 2 lines.
	require.Equal(t, (natural+maxWrapLines-1)/maxWrapLines, columnFloor(cols[0], natural))
	// Wrappable+Truncatable can shrink all the way to the truncatable floor so it
	// yields space to columns that cannot truncate.
	require.Equal(t, truncFloor, columnFloor(cols[1], natural))
	// Truncatable-only floors at the truncatable floor.
	require.Equal(t, truncFloor, columnFloor(cols[2], natural))
	// Non-shrinkable keeps its natural width.
	require.Equal(t, natural, columnFloor(cols[3], natural))
}

func TestFitColumnsWrappableYieldsToHigherPriority(t *testing.T) {
	// A wrappable+truncatable NAME (priority 2) should shrink aggressively so the
	// higher-priority truncatable STATUS (priority 1) stays as readable as
	// possible, rather than NAME hogging width and forcing STATUS to its floor.
	cols, err := parseColumns([]PrettyColumn{
		{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
		{
			Column:      Column{Heading: "NAME", ValueTemplate: "{{.Name}}"},
			Priority:    2,
			Wrappable:   true,
			Truncatable: true,
		},
		{
			Column:      Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
			Priority:    1,
			Truncatable: true,
		},
		{Column: Column{Heading: "INSTALLED", ValueTemplate: "{{.V}}"}, Priority: 1},
	})
	require.NoError(t, err)

	// ID and INSTALLED are not shrinkable.
	natural := []int{19, 37, 18, 14}
	widths := fitColumns(cols, natural, 66)

	require.LessOrEqual(t, rowWidth(widths), 66)
	require.Equal(t, 19, widths[0], "ID is not shrinkable")
	require.Equal(t, 14, widths[3], "INSTALLED is not shrinkable")
	// NAME is squeezed below its wrap floor (natural/2 = 19) to protect STATUS.
	require.Less(t, widths[1], (37+maxWrapLines-1)/maxWrapLines)
	// STATUS keeps more width than its minimum floor.
	require.Greater(t, widths[2], minVisibleChars+displayWidth(ellipsis))
	require.Greater(t, widths[2], widths[1], "STATUS should stay wider than the squeezed NAME")
}

func TestForceCardsIgnoresWidth(t *testing.T) {
	rows := []responsiveInput{
		{Id: "tmpl-a", Name: "Template A", Status: "x"},
	}
	columns := []PrettyColumn{
		{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, CardTitle: true},
		{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}},
	}
	// A very wide terminal would normally select the full table; ForceCards overrides.
	formatter := &PrettyTableFormatter{ConsoleWidthFn: func() int { return 400 }}
	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{Columns: columns, ForceCards: true})
	require.NoError(t, err)
	out := buf.String()
	// Card layout: a labeled field, not a header underline.
	require.Contains(t, out, "ID:")
	require.NotContains(t, out, "─")
}
