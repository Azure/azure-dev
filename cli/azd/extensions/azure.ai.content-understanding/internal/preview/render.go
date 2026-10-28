// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type table struct {
	caption string
	headers []string
	rows    [][]string
}

// Render writes a human-readable preview without changing the source result.
// Width controls layout selection, not a hard bound on retained long values.
func Render(ctx context.Context, writer io.Writer, source string, width int, color bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if width < 1 {
		return fmt.Errorf("preview width must be positive")
	}
	style := "ascii"
	if color {
		style = "dark"
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
	if err != nil {
		return fmt.Errorf("initialize Markdown preview: %w", err)
	}
	source = safeText(source)
	source, err = formatMetadata(source)
	if err != nil {
		return err
	}
	data := []byte(source)
	document := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(data))
	var result strings.Builder
	result.WriteString("Preview only; original Markdown/HTML is unchanged. " +
		"URLs omit credentials, queries and fragments.\n\n")
	cursor := 0
	err = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		block, ok := node.(*ast.HTMLBlock)
		if !entering || !ok || block.Lines().Len() == 0 {
			return ast.WalkContinue, nil
		}
		start := block.Lines().At(0).Start
		end := block.Lines().At(block.Lines().Len() - 1).Stop
		if block.HasClosure() {
			end = max(end, block.ClosureLine.Stop)
		}
		raw := source[start:end]
		if !strings.Contains(strings.ToLower(raw), "<table") {
			return ast.WalkSkipChildren, nil
		}
		before, err := renderer.Render(source[cursor:start])
		if err != nil {
			return ast.WalkStop, fmt.Errorf("render Markdown: %w", err)
		}
		result.WriteString(before)
		parsed, reason, err := parseTable(raw)
		if err != nil {
			return ast.WalkStop, err
		}
		if reason != "" {
			fmt.Fprintf(&result, "\n[HTML table not rendered: %s; source shown with preview safety filtering]\n%s\n",
				reason, safeText(html.UnescapeString(raw)))
		} else {
			rendered, err := renderTable(parsed, width)
			if err != nil {
				return ast.WalkStop, err
			}
			if parsed.caption != "" {
				result.WriteString(parsed.caption + "\n")
			}
			result.WriteString(rendered)
			result.WriteByte('\n')
		}
		cursor = end
		return ast.WalkSkipChildren, nil
	})
	if err != nil {
		return err
	}
	after, err := renderer.Render(source[cursor:])
	if err != nil {
		return fmt.Errorf("render Markdown: %w", err)
	}
	result.WriteString(after)
	rendered := result.String()
	if !color {
		rendered = ansi.Strip(rendered)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := io.WriteString(writer, rendered); err != nil {
		return fmt.Errorf("write rendered preview: %w", err)
	}
	return nil
}

func parseTable(source string) (table, string, error) {
	result := table{}
	nodes, err := html.ParseFragment(strings.NewReader(source),
		&html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return result, "", fmt.Errorf("parse HTML table: %w", err)
	}
	var root *html.Node
	for _, node := range nodes {
		if node.Type == html.TextNode && strings.TrimSpace(node.Data) == "" {
			continue
		}
		if node.Type != html.ElementNode || node.Data != "table" || root != nil {
			return result, "expected one standalone table", nil
		}
		root = node
	}
	if root == nil {
		return result, "expected one standalone table", nil
	}
	var visit func(*html.Node) string
	visit = func(node *html.Node) string {
		if node.Type == html.TextNode && strings.TrimSpace(node.Data) == "" {
			return ""
		}
		if node.Type != html.ElementNode || len(node.Attr) != 0 {
			return "unsupported structure or attributes"
		}
		switch node.Data {
		case "table", "thead", "tbody":
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if reason := visit(child); reason != "" {
					return reason
				}
			}
		case "caption":
			value, ok := cellText(node)
			if !ok || result.headers != nil || result.caption != "" {
				return "unsupported caption"
			}
			result.caption = value
		case "tr":
			var values []string
			header := result.headers == nil
			for cell := node.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type == html.TextNode && strings.TrimSpace(cell.Data) == "" {
					continue
				}
				if cell.Type != html.ElementNode ||
					(header && cell.Data != "th") || (!header && cell.Data != "td") {
					return "expected one header row followed by data rows"
				}
				for _, attribute := range cell.Attr {
					if (attribute.Key != "rowspan" && attribute.Key != "colspan") || attribute.Val != "1" {
						return "merged cells or unsupported cell attributes"
					}
				}
				value, ok := cellText(cell)
				if !ok || value == "" {
					return "rich or empty cells are unsupported"
				}
				values = append(values, value)
			}
			if len(values) == 0 {
				return "empty row"
			}
			if header {
				seen := map[string]bool{}
				for _, value := range values {
					if seen[value] {
						return "duplicate headings"
					}
					seen[value] = true
				}
				result.headers = values
			} else {
				if len(values) != len(result.headers) {
					return "inconsistent column counts"
				}
				result.rows = append(result.rows, values)
			}
		default:
			return "unsupported table element"
		}
		return ""
	}
	reason := visit(root)
	if reason == "" && (len(result.headers) == 0 || len(result.rows) == 0) {
		reason = "expected headers and at least one data row"
	}
	return result, reason, nil
}

func cellText(node *html.Node) (string, bool) {
	var value strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.TextNode {
			return "", false
		}
		value.WriteString(child.Data)
	}
	return strings.Join(strings.Fields(safeText(value.String())), " "), true
}

func renderTable(value table, width int) (string, error) {
	columns := make([]output.PrettyColumn, len(value.headers))
	for index, heading := range value.headers {
		columns[index] = output.PrettyColumn{
			Column: output.Column{Heading: heading, ValueTemplate: fmt.Sprintf("{{index . %d}}", index+1)}, Priority: 1,
		}
	}
	rows := make([][]string, len(value.rows))
	for index, row := range value.rows {
		rows[index] = append([]string{strconv.Itoa(index + 1)}, row...)
	}
	formatter := output.PrettyTableFormatter{ConsoleWidthFn: func() int { return width }}
	// Document values must not inherit list-command column omission or truncation.
	options := output.PrettyTableFormatterOptions{Columns: columns, FullThreshold: 1, CompactThreshold: 1}
	var result bytes.Buffer
	if err := formatter.Format(rows, &result, options); err != nil {
		return "", fmt.Errorf("render table: %w", err)
	}
	for line := range strings.SplitSeq(result.String(), "\n") {
		if ansi.StringWidth(line) <= width {
			continue
		}
		heading := "Row"
		for slices.Contains(value.headers, heading) {
			heading = "_" + heading
		}
		options.ForceCards = true
		options.Columns = append([]output.PrettyColumn{{
			Column: output.Column{Heading: heading, ValueTemplate: "Row {{index . 0}}"}, CardTitle: true,
		}}, columns...)
		result.Reset()
		if err := formatter.Format(rows, &result, options); err != nil {
			return "", fmt.Errorf("render cards: %w", err)
		}
		break
	}
	return result.String(), nil
}
