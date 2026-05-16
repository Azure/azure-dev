// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type prettyInput struct {
	Name         string
	Version      string
	Status       string
	StatusSymbol string
	Source       string
	Id           string
}

func TestPrettyTableFormatterBasic(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Name: "Extension A", Version: "1.0.0", Status: "✓ Up to date", Source: "azd"},
		{Id: "ext-b", Name: "Extension B", Version: "2.1.0", Status: "↑ Update available", Source: "azd"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, Priority: 3},
			{Column: Column{Heading: "VERSION", ValueTemplate: "{{.Version}}"}, Priority: 1},
			{Column: Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"}, Priority: 1},
			{Column: Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"}, Priority: 1},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	require.Contains(t, output, "ID")
	require.Contains(t, output, "NAME")
	require.Contains(t, output, "VERSION")
	require.Contains(t, output, "STATUS")
	require.Contains(t, output, "SOURCE")
	require.Contains(t, output, "ext-a")
	require.Contains(t, output, "ext-b")
	require.Contains(t, output, "1.0.0")
	require.Contains(t, output, "2.1.0")
}

func TestPrettyTableFormatterNoColumns(t *testing.T) {
	formatter := &PrettyTableFormatter{}

	buf := &bytes.Buffer{}
	err := formatter.Format(struct{}{}, buf, PrettyTableFormatterOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no columns were defined")
}

func TestPrettyTableFormatterInvalidOpts(t *testing.T) {
	formatter := &PrettyTableFormatter{}

	buf := &bytes.Buffer{}
	err := formatter.Format(struct{}{}, buf, "bad opts")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid formatter options")
}

func TestPrettyTableFormatterKind(t *testing.T) {
	formatter := &PrettyTableFormatter{}
	require.Equal(t, TableFormat, formatter.Kind())
}

func TestPrettyTableFormatterTransformer(t *testing.T) {
	rows := []prettyInput{
		{Id: "EXT-A", Name: "EXT-A", Version: "1.0.0", Status: "ok"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{
				Heading:       "ID",
				ValueTemplate: "{{.Id}}",
				Transformer:   strings.ToLower,
			}, Priority: 1},
		},
	})

	require.NoError(t, err)
	require.Contains(t, buf.String(), "ext-a")
}

func TestPrettyTableFormatterColorFunc(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Name: "ext-a", Version: "1.0.0", Status: "ok"},
	}

	colorApplied := false
	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{
				Column: Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
				ColorFunc: func(s string) string {
					colorApplied = true
					return "[" + s + "]"
				},
				Priority: 1,
			},
		},
	})

	require.NoError(t, err)
	require.True(t, colorApplied)
	require.Contains(t, buf.String(), "[ok]")
}

func TestPrettyTableFormatterScalar(t *testing.T) {
	row := prettyInput{Id: "single", Name: "single", Version: "0.1.0", Status: "ok"}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(row, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
		},
	})

	require.NoError(t, err)
	require.Contains(t, buf.String(), "single")
}

func TestPrettyTableFormatterNonexistentField(t *testing.T) {
	row := prettyInput{Id: "test"}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(row, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "Bad", ValueTemplate: "{{.NoSuchField}}"}, Priority: 1},
		},
	})
	require.Error(t, err)
}

func TestPrettyNoColumnSeparators(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Name: "Extension A", Version: "1.0.0", Status: "ok", Source: "azd"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{Column: Column{Heading: "VERSION", ValueTemplate: "{{.Version}}"}, Priority: 1},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	// No box-drawing column separators
	require.NotContains(t, output, "│")
	// Header underline present
	require.Contains(t, output, "─")
}

func TestPrettyHeaderUnderline(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Version: "1.0.0"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{Column: Column{Heading: "VERSION", ValueTemplate: "{{.Version}}"}, Priority: 1},
		},
	})

	require.NoError(t, err)
	lines := strings.Split(buf.String(), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	// Second line should be all ─ characters
	underline := strings.TrimSpace(lines[1])
	require.True(t, len(underline) > 0)
	for _, ch := range underline {
		require.Equal(t, '─', ch, "header underline should be all ─ chars")
	}
}

// Breakpoint tests

func TestPrettyFullBreakpoint(t *testing.T) {
	rows := []prettyInput{
		{Id: "azure.ai.agents", Name: "Foundry agents (Preview)", Version: "0.1.18-preview",
			Status: "✓ Up to date", StatusSymbol: "✓", Source: "azd"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: extListTestColumns(),
	})

	require.NoError(t, err)
	output := buf.String()

	// Full: all columns shown, full status text
	require.Contains(t, output, "ID")
	require.Contains(t, output, "NAME")
	require.Contains(t, output, "VERSION")
	require.Contains(t, output, "STATUS")
	require.Contains(t, output, "SOURCE")
	require.Contains(t, output, "✓ Up to date")
	require.Contains(t, output, "Foundry agents (Preview)")
}

func TestPrettyCompactBreakpoint(t *testing.T) {
	rows := []prettyInput{
		{Id: "azure.ai.agents", Name: "Foundry agents (Preview)", Version: "0.1.18-preview",
			Status: "✓ Up to date", StatusSymbol: "✓", Source: "azd"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 80 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: extListTestColumns(),
	})

	require.NoError(t, err)
	output := buf.String()

	// Compact: Priority ≤ 2 columns, status uses ShortValueTemplate (symbol only)
	require.Contains(t, output, "ID")
	require.Contains(t, output, "VERSION")
	require.Contains(t, output, "STATUS")
	require.Contains(t, output, "SOURCE")
	// NAME (priority 3) dropped
	require.NotContains(t, output, "NAME")
	// Should use short status template (symbol only)
	require.NotContains(t, output, "Up to date")
}

func TestPrettyCardBreakpoint(t *testing.T) {
	rows := []prettyInput{
		{Id: "azure.ai.agents", Name: "Foundry agents (Preview)", Version: "0.1.18-preview",
			Status: "✓ Up to date", StatusSymbol: "✓", Source: "azd"},
		{Id: "azure.coding-agent", Name: "Coding agent config", Version: "0.6.1",
			Status: "⚠ Incompatible", StatusSymbol: "⚠", Source: "local"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 40 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns:         extListTestColumns(),
		CardGroupColumn: "SOURCE",
	})

	require.NoError(t, err)
	output := buf.String()

	// Cards grouped by source
	require.Contains(t, output, "── azd ")
	require.Contains(t, output, "── local ")
	// Card body has key-value pairs (using column headings as keys)
	require.Contains(t, output, "NAME:")
	require.Contains(t, output, "ID:")
	require.Contains(t, output, "VERSION:")
	require.Contains(t, output, "STATUS:")
	// SOURCE should NOT appear in card body (it's the group header)
	require.NotContains(t, output, "SOURCE:")
}

func TestPrettyCardGroupingOrder(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-1", Name: "First", Version: "1.0", Status: "✓", Source: "azd"},
		{Id: "ext-2", Name: "Second", Version: "2.0", Status: "✓", Source: "local"},
		{Id: "ext-3", Name: "Third", Version: "3.0", Status: "✓", Source: "azd"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 40 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, Priority: 1},
			{Column: Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"}, Priority: 4},
		},
		CardGroupColumn: "SOURCE",
	})

	require.NoError(t, err)
	output := buf.String()

	// azd appears first (first row's source)
	azdIdx := strings.Index(output, "── azd ")
	localIdx := strings.Index(output, "── local ")
	require.Greater(t, localIdx, azdIdx, "azd group should appear before local group")

	// Both extensions with source=azd should be in the azd group
	require.Contains(t, output, "First")
	require.Contains(t, output, "Third")
}

func TestPrettyShortValueTemplate(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Status: "✓ Up to date", StatusSymbol: "✓"},
	}

	// Width 70 is in compact range (60-99), should use short template
	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 70 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{
				Column:             Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
				Priority:           1,
				ShortValueTemplate: "{{.StatusSymbol}}",
			},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	// At compact width, should use short template (symbol only)
	require.NotContains(t, output, "Up to date")
}

func TestPrettyShortValueTemplateNotUsedAtFullWidth(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Status: "✓ Up to date", StatusSymbol: "✓"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{
				Column:             Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
				Priority:           1,
				ShortValueTemplate: "{{.StatusSymbol}}",
			},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	// At full width, should use full template
	require.Contains(t, output, "✓ Up to date")
}

func TestPrettyColorFuncWithShortValueTemplate(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Status: "✓ Up to date", StatusSymbol: "✓"},
	}

	colorApplied := false
	// Width 70 is compact range
	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 70 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{
				Column:             Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
				Priority:           1,
				ShortValueTemplate: "{{.StatusSymbol}}",
				ColorFunc: func(s string) string {
					colorApplied = true
					return "[" + s + "]"
				},
			},
		},
	})

	require.NoError(t, err)
	require.True(t, colorApplied, "ColorFunc should be applied to short value")
	output := buf.String()
	// At compact width, should use short template AND apply color
	require.NotContains(t, output, "Up to date")
	require.Contains(t, output, "[✓]")
}

// Breakpoint boundary transition tests

func TestPrettyBreakpointTransitions(t *testing.T) {
	rows := []prettyInput{
		{Id: "azure.ai.agents", Name: "Foundry agents (Preview)", Version: "0.1.18-preview",
			Status: "✓ Up to date", StatusSymbol: "✓", Source: "azd"},
	}

	columns := extListTestColumns()

	tests := []struct {
		name     string
		width    int
		wantCard bool
		wantNAME bool
	}{
		{"full at 120", 120, false, true},
		{"full at 100", 100, false, true},
		{"compact at 99", 99, false, false},
		{"compact at 60", 60, false, false},
		{"card at 59", 59, true, false},
		{"card at 40", 40, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := &PrettyTableFormatter{
				ConsoleWidthFn: func() int { return tt.width },
			}

			buf := &bytes.Buffer{}
			err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
				Columns:         columns,
				CardGroupColumn: "SOURCE",
			})
			require.NoError(t, err)
			output := buf.String()

			if tt.wantCard {
				require.Contains(t, output, "── azd ", "expected card layout for width %d", tt.width)
			} else {
				require.Contains(t, output, "─", "expected header underline for width %d", tt.width)
			}

			if !tt.wantCard {
				if tt.wantNAME {
					require.Contains(t, output, "NAME", "expected NAME column for width %d", tt.width)
				} else {
					require.NotContains(t, output, "NAME", "expected no NAME column for width %d", tt.width)
				}
			}
		})
	}
}

func TestPrettyCardColorFunc(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Name: "Extension A", Version: "1.0.0", Status: "ok", Source: "azd"},
	}

	colorApplied := false
	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 40 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, Priority: 1},
			{
				Column: Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
				ColorFunc: func(s string) string {
					colorApplied = true
					return "«" + s + "»"
				},
				Priority: 2,
			},
			{Column: Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"}, Priority: 4},
		},
		CardGroupColumn: "SOURCE",
	})

	require.NoError(t, err)
	require.True(t, colorApplied, "ColorFunc should be called in card layout")
	require.Contains(t, buf.String(), "«ok»")
}

func TestPrettyUngroupedCards(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Name: "Extension A", Version: "1.0.0", Status: "ok"},
		{Id: "ext-b", Name: "Extension B", Version: "2.0.0", Status: "ok"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 40 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, Priority: 1},
			{Column: Column{Heading: "VERSION", ValueTemplate: "{{.Version}}"}, Priority: 1},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	// Ungrouped cards use box-drawing borders
	require.Contains(t, output, "┌")
	require.Contains(t, output, "└")
	require.Contains(t, output, "Extension A")
	require.Contains(t, output, "Extension B")
}

func TestPrettyEmptyData(t *testing.T) {
	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format([]prettyInput{}, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, Priority: 1},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	// Should produce header and underline but no data rows
	require.Contains(t, output, "ID")
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	require.LessOrEqual(t, len(lines), 2, "expected at most header + underline for empty data")
}

func TestPrettySingleGroupCards(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-1", Name: "First", Version: "1.0", Status: "✓", Source: "azd"},
		{Id: "ext-2", Name: "Second", Version: "2.0", Status: "✓", Source: "azd"},
		{Id: "ext-3", Name: "Third", Version: "3.0", Status: "✓", Source: "azd"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 40 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, Priority: 1},
			{Column: Column{Heading: "VERSION", ValueTemplate: "{{.Version}}"}, Priority: 1},
			{Column: Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"}, Priority: 4},
		},
		CardGroupColumn: "SOURCE",
	})

	require.NoError(t, err)
	output := buf.String()

	// Only one group header should appear
	require.Equal(t, 1, strings.Count(output, "── azd "), "expected exactly one group header")

	// All items should be present
	require.Contains(t, output, "First")
	require.Contains(t, output, "Second")
	require.Contains(t, output, "Third")

	// No extra group separators (only one "──" line at the top)
	headerLines := 0
	for line := range strings.SplitSeq(output, "\n") {
		if strings.HasPrefix(line, "── ") {
			headerLines++
		}
	}
	require.Equal(t, 1, headerLines, "expected exactly one group header line")
}

func TestPrettyTableANSIAlignment(t *testing.T) {
	// Simulate ANSI color codes with varying lengths to verify
	// that column alignment uses display width, not byte length.
	green := func(s string) string { return "\033[32m" + s + "\033[0m" }
	gray := func(s string) string { return "\033[90m" + s + "\033[0m" }

	rows := []prettyInput{
		{Id: "ext-a", Status: "✓ Up to date", Source: "azd"},
		{Id: "ext-b", Status: "-", Source: "local"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 120 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{
				Column:   Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
				Priority: 1,
				ColorFunc: func(s string) string {
					if s == "-" {
						return gray(s)
					}
					return green(s)
				},
			},
			{Column: Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"}, Priority: 1},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	// Parse data rows (skip header and underline)
	lines := strings.Split(output, "\n")
	require.GreaterOrEqual(t, len(lines), 4, "expected header + underline + 2 data rows")

	// Strip ANSI codes to find the visual position of SOURCE column
	strip := func(s string) string {
		return ansiRegex.ReplaceAllString(s, "")
	}

	// displayIndex returns the display-column position of substr within s,
	// where s has already been stripped of ANSI codes.
	displayIndex := func(s, substr string) int {
		before, _, ok := strings.Cut(s, substr)
		if !ok {
			return -1
		}
		return displayWidth(before)
	}

	row1Stripped := strip(lines[2])
	row2Stripped := strip(lines[3])

	// Both SOURCE values should start at the same display column.
	sourcePos1 := displayIndex(row1Stripped, "azd")
	sourcePos2 := displayIndex(row2Stripped, "local")

	require.Greater(t, sourcePos1, 0, "SOURCE value 'azd' not found in row 1")
	require.Greater(t, sourcePos2, 0, "SOURCE value 'local' not found in row 2")
	require.Equal(t, sourcePos1, sourcePos2,
		"SOURCE column should start at the same position in both rows;\n"+
			"row1: %q\nrow2: %q", row1Stripped, row2Stripped)
}

func TestDisplayWidth(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"ascii", "hello", 5},
		{"empty", "", 0},
		{"checkmark", "✓ Up to date", 12},
		{"dash", "-", 1},
		{"ansi green", "\033[32m✓ Up to date\033[0m", 12},
		{"ansi gray", "\033[90m-\033[0m", 1},
		{"no ansi unicode", "日本語", 6},
		{"ansi with unicode", "\033[31m日本語\033[0m", 6},
		{"osc8 hyperlink ST", "\x1b]8;;https://example.com\x1b\\Click Here\x1b]8;;\x1b\\", 10},
		{"osc8 hyperlink BEL", "\x1b]8;;https://example.com\aClick Here\x1b]8;;\a", 10},
		{"osc8 with ansi color", "\033[36m\x1b]8;;https://example.com\x1b\\Click Here\x1b]8;;\x1b\\\033[0m", 10},
		{"plain text unchanged", "no escapes here", 15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := displayWidth(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}

// extListTestColumns returns the standard 5-column config matching the 2-threshold spec.
func extListTestColumns() []PrettyColumn {
	return []PrettyColumn{
		{
			Column:   Column{Heading: "ID", ValueTemplate: "{{.Id}}"},
			Priority: 1,
		},
		{
			Column:   Column{Heading: "NAME", ValueTemplate: "{{.Name}}"},
			Priority: 3,
		},
		{
			Column:   Column{Heading: "VERSION", ValueTemplate: "{{.Version}}"},
			Priority: 1,
		},
		{
			Column:             Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
			Priority:           2,
			ShortValueTemplate: "{{.StatusSymbol}}",
		},
		{
			Column:   Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"},
			Priority: 1,
		},
	}
}
