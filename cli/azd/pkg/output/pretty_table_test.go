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
			{Column: Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"}, Priority: 2},
			{Column: Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"}, Priority: 4},
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

func TestPrettyWideBreakpoint(t *testing.T) {
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

	// Wide: all columns shown, full status text
	require.Contains(t, output, "ID")
	require.Contains(t, output, "NAME")
	require.Contains(t, output, "VERSION")
	require.Contains(t, output, "STATUS")
	require.Contains(t, output, "SOURCE")
	require.Contains(t, output, "✓ Up to date")
	require.Contains(t, output, "Foundry agents (Preview)")
}

func TestPrettyMediumBreakpoint(t *testing.T) {
	rows := []prettyInput{
		{Id: "azure.ai.agents", Name: "Foundry agents (Preview)", Version: "0.1.18-preview",
			Status: "✓ Up to date", StatusSymbol: "✓", Source: "azd"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 95 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: extListTestColumns(),
	})

	require.NoError(t, err)
	output := buf.String()

	// Medium: all columns shown, status uses ShortValueTemplate (symbol only)
	require.Contains(t, output, "ID")
	require.Contains(t, output, "NAME")
	require.Contains(t, output, "SOURCE")
	// Should use short status template (symbol only)
	require.NotContains(t, output, "Up to date")
}

func TestPrettyNarrowBreakpoint(t *testing.T) {
	rows := []prettyInput{
		{Id: "azure.ai.agents", Name: "Foundry agents (Preview)", Version: "0.1.18-preview",
			Status: "✓ Up to date", StatusSymbol: "✓", Source: "azd"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 60 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: extListTestColumns(),
	})

	require.NoError(t, err)
	output := buf.String()

	// Narrow: only Priority ≤ 2 columns (ID, VERSION, STATUS)
	require.Contains(t, output, "ID")
	require.Contains(t, output, "VERSION")
	require.Contains(t, output, "STATUS")
	// NAME (priority 3) and SOURCE (priority 4) dropped
	require.NotContains(t, output, "NAME")
	require.NotContains(t, output, "SOURCE")
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

func TestPrettyTruncation(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Name: "A Very Long Extension Name That Should Get Truncated Here", Version: "1.0.0",
			Status: "ok", StatusSymbol: "✓", Source: "azd"},
	}

	// Use width 80 (medium) — content is wider than 80 so truncation is needed
	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 80 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{Column: Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, Priority: 3, Truncatable: true},
			{Column: Column{Heading: "VERSION", ValueTemplate: "{{.Version}}"}, Priority: 1},
			{Column: Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"}, Priority: 2},
			{Column: Column{Heading: "SOURCE", ValueTemplate: "{{.Source}}"}, Priority: 4},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	// Name should be truncated with ellipsis at medium width
	require.Contains(t, output, "…", "truncated name should contain ellipsis")
	// The full name should NOT appear
	require.NotContains(t, output, "A Very Long Extension Name That Should Get Truncated Here")
}

func TestPrettyShortValueTemplate(t *testing.T) {
	rows := []prettyInput{
		{Id: "ext-a", Status: "✓ Up to date", StatusSymbol: "✓"},
	}

	formatter := &PrettyTableFormatter{
		ConsoleWidthFn: func() int { return 90 },
	}

	buf := &bytes.Buffer{}
	err := formatter.Format(rows, buf, PrettyTableFormatterOptions{
		Columns: []PrettyColumn{
			{Column: Column{Heading: "ID", ValueTemplate: "{{.Id}}"}, Priority: 1},
			{
				Column:             Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
				Priority:           2,
				ShortValueTemplate: "{{.StatusSymbol}}",
			},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	// At medium width, should use short template (symbol only)
	require.NotContains(t, output, "Up to date")
}

func TestPrettyShortValueTemplateNotUsedAtWideWidth(t *testing.T) {
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
				Priority:           2,
				ShortValueTemplate: "{{.StatusSymbol}}",
			},
		},
	})

	require.NoError(t, err)
	output := buf.String()

	// At wide width, should use full template
	require.Contains(t, output, "✓ Up to date")
}

// Breakpoint boundary transition tests

func TestPrettyBreakpointTransitions(t *testing.T) {
	rows := []prettyInput{
		{Id: "azure.ai.agents", Name: "Foundry agents (Preview)", Version: "0.1.18-preview",
			Status: "✓ Up to date", StatusSymbol: "✓", Source: "azd"},
	}

	columns := extListTestColumns()

	tests := []struct {
		name       string
		width      int
		wantCard   bool
		wantNAME   bool
		wantSOURCE bool
	}{
		{"wide at 110", 110, false, true, true},
		{"medium at 109", 109, false, true, true},
		{"medium at 80", 80, false, true, true},
		{"narrow at 79", 79, false, false, false},
		{"narrow at 50", 50, false, false, false},
		{"card at 49", 49, true, false, false},
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
				if tt.wantSOURCE {
					require.Contains(t, output, "SOURCE", "expected SOURCE column for width %d", tt.width)
				} else {
					require.NotContains(t, output, "SOURCE",
						"expected no SOURCE column for width %d", tt.width)
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

func TestTruncateWithEllipsis(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"ab", 1, "…"},
		{"abc", 3, "abc"},
		{"abcd", 3, "ab…"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := truncateWithEllipsis(tt.input, tt.maxLen)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEstimateTableWidth(t *testing.T) {
	cols := []parsedCol{
		{col: PrettyColumn{Column: Column{Heading: "Name"}}},
		{col: PrettyColumn{Column: Column{Heading: "Version"}}},
	}

	// No rows: width = len("Name") + len("Version") + 3 (padding) = 4 + 7 + 3 = 14
	width := estimateTableWidth(cols, nil)
	require.Equal(t, 14, width)
}

func TestResponsiveFilterDropsHighestPriorityNumber(t *testing.T) {
	cols := []parsedCol{
		{col: PrettyColumn{Column: Column{Heading: "Id", ValueTemplate: "{{.Name}}"}, Priority: 1}},
		{col: PrettyColumn{Column: Column{Heading: "Source-Column", ValueTemplate: "{{.Name}}"}, Priority: 4}},
		{col: PrettyColumn{Column: Column{Heading: "Name-Column", ValueTemplate: "{{.Name}}"}, Priority: 3}},
	}

	// Width so narrow that it can't fit all 3 columns
	result := responsiveFilter(cols, nil, 20)

	require.Less(t, len(result), 3)

	// Id (priority 1) should always remain
	hasId := false
	for _, c := range result {
		if c.col.Heading == "Id" {
			hasId = true
		}
	}
	require.True(t, hasId, "Priority 1 columns should be kept the longest")
}

func TestResponsiveFilterKeepsAllWhenFits(t *testing.T) {
	cols := []parsedCol{
		{col: PrettyColumn{Column: Column{Heading: "Id", ValueTemplate: "{{.Name}}"}, Priority: 1}},
		{col: PrettyColumn{Column: Column{Heading: "Source", ValueTemplate: "{{.Name}}"}, Priority: 4}},
	}

	result := responsiveFilter(cols, nil, 200)
	require.Len(t, result, 2)
}

// extListTestColumns returns the standard 5-column config matching the hybrid UX spec.
func extListTestColumns() []PrettyColumn {
	return []PrettyColumn{
		{
			Column:      Column{Heading: "ID", ValueTemplate: "{{.Id}}"},
			Priority:    1,
			Truncatable: true,
		},
		{
			Column:      Column{Heading: "NAME", ValueTemplate: "{{.Name}}"},
			Priority:    3,
			Truncatable: true,
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
			Priority: 4,
		},
	}
}
