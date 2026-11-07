// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
	terminalColor "github.com/fatih/color"
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

type previewBlock struct {
	start, end int
	table      bool
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
	color = color && os.Getenv("NO_COLOR") == ""
	tableColor := terminalColor.New(terminalColor.FgCyan)
	commentColor := terminalColor.New(terminalColor.FgHiBlack)
	warningColor := terminalColor.New(terminalColor.FgYellow)
	tableColor.EnableColor()
	commentColor.EnableColor()
	warningColor.EnableColor()
	style := styles.ASCIIStyleConfig
	if color {
		style = styles.DarkStyleConfig
	}
	style.H1 = style.H2
	style.H1.Prefix = "# "
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style), glamour.WithWordWrap(width), glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return fmt.Errorf("initialize Markdown preview: %w", err)
	}
	source = safeText(source)
	metadata, err := metadataBlocks(source)
	if err != nil {
		return err
	}
	data := []byte(source)
	var blocks []previewBlock
	for _, segment := range metadata {
		blocks = append(blocks, previewBlock{start: segment.Start, end: segment.Stop})
		for index := segment.Start; index < segment.Stop; index++ {
			if data[index] != '\n' {
				data[index] = ' '
			}
		}
	}
	document := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(data))
	var references strings.Builder
	err = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		if _, ok := node.(*ast.LinkReferenceDefinition); entering && ok {
			references.Write(node.Lines().Value(data))
			references.WriteString("\n\n")
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return err
	}
	// Each fragment needs the document's definitions in source order so links
	// resolve across tables and duplicate labels retain their first definition.
	renderMarkdown := func(fragment string) (string, error) {
		return renderer.Render(references.String() + fragment)
	}
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
		isComment := strings.HasPrefix(strings.TrimSpace(raw), "<!--")
		isTable := !isComment && strings.Contains(strings.ToLower(raw), "<table")
		if !isTable && !isComment {
			return ast.WalkSkipChildren, nil
		}
		if isTable {
			blocks = append(blocks, tableBlocks(raw, start)...)
		} else {
			blocks = append(blocks, previewBlock{start: start, end: end})
		}
		return ast.WalkSkipChildren, nil
	})
	if err != nil {
		return err
	}
	slices.SortFunc(blocks, func(a, b previewBlock) int { return cmp.Compare(a.start, b.start) })
	var result strings.Builder
	cursor := 0
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		before, err := renderMarkdown(source[cursor:block.start])
		if err != nil {
			return fmt.Errorf("render Markdown: %w", err)
		}
		result.WriteString(before)
		raw := source[block.start:block.end]
		if !block.table {
			if color {
				raw = commentColor.Sprint(raw)
			}
			result.WriteString(raw)
			cursor = block.end
			continue
		}
		parsed, reason, err := parseTable(raw)
		if err != nil {
			return err
		}
		if reason != "" {
			notice := fmt.Sprintf("<!-- HTML table not rendered: %s; original HTML shown. -->", reason)
			if color {
				notice = warningColor.Sprint(notice)
			}
			fmt.Fprintf(&result, "\n%s\n%s\n", notice, raw)
		} else {
			rendered, cards, err := renderTable(parsed, width)
			if err != nil {
				return err
			}
			if cards {
				notice := "<!-- Table exceeds terminal width; showing each row as a card. -->"
				if color {
					notice = warningColor.Sprint(notice)
				}
				result.WriteString(notice + "\n")
			}
			if parsed.caption != "" {
				rendered = parsed.caption + "\n" + rendered
			}
			if color {
				var styled strings.Builder
				for line := range strings.SplitAfterSeq(ansi.Strip(rendered), "\n") {
					if cards && strings.HasPrefix(strings.TrimSpace(line), "<!-- Row ") {
						styled.WriteString(commentColor.Sprint(line))
					} else {
						styled.WriteString(tableColor.Sprint(line))
					}
				}
				rendered = styled.String()
			}
			result.WriteString(rendered)
			result.WriteByte('\n')
		}
		cursor = block.end
	}
	after, err := renderMarkdown(source[cursor:])
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

// tableBlocks separates tables from following footnotes within the same Markdown HTML block.
func tableBlocks(source string, offset int) []previewBlock {
	var blocks []previewBlock
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	position, start, depth := offset, offset, 0
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			break
		}
		tokenStart := position
		position += len(tokenizer.Raw())
		token := tokenizer.Token()
		switch {
		case (kind == html.StartTagToken || kind == html.SelfClosingTagToken) && token.Data == "table":
			if depth == 0 {
				start = tokenStart
			}
			depth++
		case kind == html.EndTagToken && token.Data == "table" && depth > 0:
			depth--
			if depth == 0 {
				blocks = append(blocks, previewBlock{start: start, end: position, table: true})
			}
		case kind == html.CommentToken && depth == 0:
			blocks = append(blocks, previewBlock{start: tokenStart, end: position})
		case depth == 0 && kind != html.TextToken:
			return []previewBlock{{start: offset, end: offset + len(source), table: true}}
		}
	}
	if depth > 0 {
		blocks = append(blocks, previewBlock{start: start, end: offset + len(source), table: true})
	}
	if len(blocks) == 0 {
		return []previewBlock{{start: offset, end: offset + len(source), table: true}}
	}
	return blocks
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
	columnCount := 0
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
			if !ok || columnCount != 0 || result.caption != "" {
				return "unsupported caption"
			}
			result.caption = value
		case "tr":
			var values []string
			header := false
			for cell := node.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type == html.TextNode && strings.TrimSpace(cell.Data) == "" {
					continue
				}
				if cell.Type != html.ElementNode || (cell.Data != "th" && cell.Data != "td") {
					return "unsupported row structure"
				}
				if len(values) == 0 {
					header = cell.Data == "th"
				}
				if (cell.Data == "th") != header || (header && columnCount != 0) {
					return "multiple or mixed header rows are unsupported"
				}
				for _, attribute := range cell.Attr {
					if (attribute.Key != "rowspan" && attribute.Key != "colspan") || attribute.Val != "1" {
						return "merged cells or unsupported cell attributes"
					}
				}
				value, ok := cellText(cell)
				if !ok {
					return "unsupported cell content"
				}
				values = append(values, value)
			}
			if len(values) == 0 {
				return "empty row"
			}
			if columnCount == 0 {
				columnCount = len(values)
			} else if len(values) != columnCount {
				return "inconsistent column counts"
			}
			if header {
				result.headers = values
			} else {
				result.rows = append(result.rows, values)
			}
		default:
			return "unsupported table element"
		}
		return ""
	}
	reason := visit(root)
	if reason == "" && columnCount == 0 {
		reason = "table has no cells"
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

func renderTable(value table, width int) (string, bool, error) {
	uniqueHeaders := len(value.headers) != 0
	seen := map[string]bool{}
	for _, heading := range value.headers {
		if heading == "" || seen[heading] {
			uniqueHeaders = false
		}
		seen[heading] = true
	}
	var rendered string
	if uniqueHeaders {
		columns := make([]output.PrettyColumn, len(value.headers))
		for index, heading := range value.headers {
			columns[index] = output.PrettyColumn{
				Column: output.Column{Heading: heading, ValueTemplate: fmt.Sprintf("{{index . %d}}", index)}, Priority: 1,
			}
		}
		formatter := output.PrettyTableFormatter{ConsoleWidthFn: func() int { return width }}
		// Document values must not inherit list-command column omission or truncation.
		options := output.PrettyTableFormatterOptions{Columns: columns, FullThreshold: 1, CompactThreshold: 1}
		var result bytes.Buffer
		if err := formatter.Format(value.rows, &result, options); err != nil {
			return "", false, fmt.Errorf("render table: %w", err)
		}
		rendered = result.String()
	} else {
		// The shared list formatter requires unique headings; document columns are positional.
		rendered = renderPositionalTable(value)
	}
	for line := range strings.SplitSeq(rendered, "\n") {
		if ansi.StringWidth(line) > width && len(value.rows) > 0 {
			return renderTableCards(value, uniqueHeaders), true, nil
		}
	}
	return rendered, false, nil
}

func renderPositionalTable(value table) string {
	columnCount := len(value.headers)
	if columnCount == 0 {
		columnCount = len(value.rows[0])
	}
	widths := make([]int, columnCount)
	for index, heading := range value.headers {
		widths[index] = ansi.StringWidth(heading)
	}
	for _, row := range value.rows {
		for index, cell := range row {
			widths[index] = max(widths[index], ansi.StringWidth(cell))
		}
	}
	var result strings.Builder
	writeRow := func(row []string) {
		for index, cell := range row {
			result.WriteString(cell)
			if index < columnCount-1 {
				result.WriteString(strings.Repeat(" ", widths[index]-ansi.StringWidth(cell)+3))
			}
		}
		result.WriteByte('\n')
	}
	if value.headers != nil {
		writeRow(value.headers)
		lineWidth := 3 * (columnCount - 1)
		for _, columnWidth := range widths {
			lineWidth += columnWidth
		}
		result.WriteString(strings.Repeat("─", lineWidth) + "\n")
	}
	for _, row := range value.rows {
		writeRow(row)
	}
	return result.String()
}

func renderTableCards(value table, uniqueHeaders bool) string {
	columnCount := len(value.rows[0])
	labels := make([]string, columnCount)
	maxLabelLen := 0
	for index := range columnCount {
		if value.headers != nil {
			labels[index] = value.headers[index]
		}
		if !uniqueHeaders {
			labels[index] = strings.TrimSpace(fmt.Sprintf("<!-- Column %d --> %s", index+1, labels[index]))
		}
		maxLabelLen = max(maxLabelLen, len(labels[index]))
	}
	var result strings.Builder
	for index, row := range value.rows {
		if index > 0 {
			result.WriteByte('\n')
		}
		fmt.Fprintf(&result, "<!-- Row %d -->\n", index+1)
		for column, cell := range row {
			fmt.Fprintf(&result, "%s:%s%s\n", labels[column],
				strings.Repeat(" ", maxLabelLen-len(labels[column])+2), cell)
		}
	}
	return result.String()
}
