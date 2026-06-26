// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"
)

// cardRow holds the resolved values for a single row in the card layout.
type cardRow struct {
	values   map[string]string // heading -> value (Transformer applied)
	colored  map[string]string // heading -> colored value (ColorFunc applied)
	groupVal string
}

// formatGroupedCards renders rows as cards. When CardGroupColumn is set, cards
// are grouped under a gray "── LABEL: value ──" header. A column flagged
// CardTitle is rendered as the card's highlighted title line and excluded from
// the labeled fields; without a CardTitle column the legacy boxed layout is used
// for ungrouped cards.
func (f *PrettyTableFormatter) formatGroupedCards(
	parsed []resolvedColumn, rows []any, termWidth int, writer io.Writer, options PrettyTableFormatterOptions,
) error {
	groupColIdx := -1
	if options.CardGroupColumn != "" {
		for i, rc := range parsed {
			if rc.col.Heading == options.CardGroupColumn {
				groupColIdx = i
				break
			}
		}
		if groupColIdx < 0 {
			return fmt.Errorf("CardGroupColumn %q does not match any column heading", options.CardGroupColumn)
		}
	}

	titleColIdx := -1
	for i, rc := range parsed {
		if rc.col.CardTitle {
			titleColIdx = i
			break
		}
	}

	allRows, err := resolveCardRows(parsed, rows, groupColIdx)
	if err != nil {
		return err
	}

	// Card field order excludes the group column and the title column.
	var cardFields []resolvedColumn
	for i, rc := range parsed {
		if i == groupColIdx || i == titleColIdx {
			continue
		}
		cardFields = append(cardFields, rc)
	}
	maxHeadingLen := 0
	for _, cf := range cardFields {
		maxHeadingLen = max(maxHeadingLen, len(cf.col.Heading))
	}

	titleHeading := ""
	if titleColIdx >= 0 {
		titleHeading = parsed[titleColIdx].col.Heading
	}

	var buf bytes.Buffer
	if groupColIdx >= 0 {
		f.writeGroupedCards(
			&buf, allRows, cardFields, parsed[groupColIdx].col.Heading,
			titleHeading, maxHeadingLen, termWidth,
		)
	} else if titleColIdx >= 0 {
		f.writeTitledCards(&buf, allRows, cardFields, titleHeading, maxHeadingLen)
	} else {
		f.writeBoxedCards(&buf, allRows, parsed, maxHeadingLen, termWidth)
	}

	_, err = fmt.Fprint(writer, buf.String())
	return err
}

// resolveCardRows executes the card templates (preferring CardValueTemplate) for
// each row and applies Transformer/ColorFunc.
func resolveCardRows(parsed []resolvedColumn, rows []any, groupColIdx int) ([]cardRow, error) {
	allRows := make([]cardRow, len(rows))
	for ri, row := range rows {
		rd := cardRow{values: make(map[string]string), colored: make(map[string]string)}
		for _, rc := range parsed {
			tmpl := rc.tmpl
			if rc.cardTmpl != nil {
				tmpl = rc.cardTmpl
			}
			val, err := prettyExecTemplate(tmpl, row)
			if err != nil {
				return nil, fmt.Errorf("row %d, column %q: %w", ri, rc.col.Heading, err)
			}
			if rc.col.Transformer != nil {
				val = rc.col.Transformer(val)
			}
			if rc.col.ColorFunc != nil {
				rd.colored[rc.col.Heading] = rc.col.ColorFunc(val)
			}
			rd.values[rc.col.Heading] = val
		}
		if groupColIdx >= 0 {
			rd.groupVal = rd.values[parsed[groupColIdx].col.Heading]
		}
		allRows[ri] = rd
	}
	return allRows, nil
}

// writeGroupedCards renders cards grouped under gray "── LABEL: value ──" headers.
func (f *PrettyTableFormatter) writeGroupedCards(
	buf *bytes.Buffer, allRows []cardRow, cardFields []resolvedColumn,
	groupHeading, titleHeading string, maxHeadingLen, termWidth int,
) {
	var groupOrder []string
	groupRows := make(map[string][]cardRow)
	for _, rd := range allRows {
		if _, ok := groupRows[rd.groupVal]; !ok {
			groupOrder = append(groupOrder, rd.groupVal)
		}
		groupRows[rd.groupVal] = append(groupRows[rd.groupVal], rd)
	}

	for gi, group := range groupOrder {
		headerText := "── " + groupHeading + ": " + stripTerminalEscapes(group) + " "
		remaining := max(termWidth-displayWidth(headerText), 1)
		buf.WriteString(WithGrayFormat("%s", headerText+strings.Repeat("─", remaining)))
		buf.WriteString("\n\n")

		rowsInGroup := groupRows[group]
		for ri, rd := range rowsInGroup {
			writeCardBody(buf, rd, cardFields, titleHeading, maxHeadingLen)
			if ri < len(rowsInGroup)-1 {
				buf.WriteByte('\n')
			}
		}
		if gi < len(groupOrder)-1 {
			buf.WriteByte('\n')
		}
	}
}

// writeTitledCards renders ungrouped, borderless cards with a highlighted title.
func (f *PrettyTableFormatter) writeTitledCards(
	buf *bytes.Buffer, allRows []cardRow, cardFields []resolvedColumn, titleHeading string, maxHeadingLen int,
) {
	for ri, rd := range allRows {
		writeCardBody(buf, rd, cardFields, titleHeading, maxHeadingLen)
		if ri < len(allRows)-1 {
			buf.WriteByte('\n')
		}
	}
}

// writeCardBody writes a single card: an optional highlighted title line followed
// by aligned "HEADING: value" fields, skipping empty values.
func writeCardBody(
	buf *bytes.Buffer, rd cardRow, cardFields []resolvedColumn, titleHeading string, maxHeadingLen int,
) {
	if titleHeading != "" {
		if title := rd.values[titleHeading]; title != "" {
			buf.WriteString(WithHighLightFormat("%s", stripTerminalEscapes(title)))
			buf.WriteByte('\n')
		}
	}
	for _, cf := range cardFields {
		val := rd.values[cf.col.Heading]
		if val == "" {
			continue
		}
		if colored, ok := rd.colored[cf.col.Heading]; ok {
			val = colored
		}
		buf.WriteString(cf.col.Heading)
		buf.WriteString(":")
		buf.WriteString(strings.Repeat(" ", maxHeadingLen-len(cf.col.Heading)+2))
		buf.WriteString(val)
		buf.WriteByte('\n')
	}
}

// writeBoxedCards renders the legacy boxed ungrouped card layout used when no
// CardTitle column is configured.
func (f *PrettyTableFormatter) writeBoxedCards(
	buf *bytes.Buffer, allRows []cardRow, parsed []resolvedColumn, maxHeadingLen, termWidth int,
) {
	boldTitle := color.New(color.Bold, color.FgHiWhite)
	titleHeading := parsed[0].col.Heading
	for ri, rd := range allRows {
		borderWidth := min(max(termWidth-2, 20), 76)
		buf.WriteString(WithGrayFormat("┌" + strings.Repeat("─", borderWidth)))
		buf.WriteByte('\n')
		buf.WriteString("│ ")
		buf.WriteString(boldTitle.Sprint(rd.values[titleHeading]))
		buf.WriteByte('\n')

		for _, rc := range parsed {
			if rc.col.Heading == titleHeading {
				continue
			}
			val := rd.values[rc.col.Heading]
			if val == "" {
				continue
			}
			if colored, ok := rd.colored[rc.col.Heading]; ok {
				val = colored
			}
			buf.WriteString("│ ")
			buf.WriteString(rc.col.Heading)
			buf.WriteString(": ")
			buf.WriteString(strings.Repeat(" ", maxHeadingLen-len(rc.col.Heading)))
			buf.WriteString(val)
			buf.WriteByte('\n')
		}

		buf.WriteString(WithGrayFormat("└" + strings.Repeat("─", borderWidth)))
		buf.WriteByte('\n')
		if ri < len(allRows)-1 {
			buf.WriteByte('\n')
		}
	}
}
