// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"fmt"
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v3"
)

var (
	metadataStart = regexp.MustCompile(`(?:\A|\n\n\*{5}\n\n)---\n`)
	metadataEnd   = regexp.MustCompile(`(?m)^---(?:\n|$)`)
)

// metadataBlocks recognizes the RenderLLM content boundaries, not arbitrary
// horizontal rules. Masking candidate YAML keeps its opaque values out of the
// Markdown parser while retaining fences surrounding literal examples.
func metadataBlocks(source string) ([]text.Segment, error) {
	var blocks []text.Segment
	masked := []byte(source)
	for _, match := range metadataStart.FindAllStringIndex(source, -1) {
		start := match[1] - len("---\n")
		if len(blocks) != 0 && start < blocks[len(blocks)-1].Stop {
			continue
		}
		body := match[1]
		closing := metadataEnd.FindStringIndex(source[body:])
		if closing == nil {
			continue
		}
		end := body + closing[1]
		content := source[body : body+closing[0]]
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(content), &node); err != nil {
			continue
		}
		if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode ||
			len(node.Content[0].Content) < 2 || node.Content[0].Content[0].Value != "mimeType" {
			continue
		}
		blocks = append(blocks, text.NewSegment(start, end))
		for index := start; index < end; index++ {
			if masked[index] != '\n' {
				masked[index] = ' '
			}
		}
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	document := goldmark.New().Parser().Parse(text.NewReader(masked))
	var protected []text.Segment
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.HTMLBlock:
			if lines := node.Lines(); lines.Len() != 0 {
				protected = append(protected, text.NewSegment(lines.At(0).Start, lines.At(lines.Len()-1).Stop))
			}
			return ast.WalkSkipChildren, nil
		default:
			return ast.WalkContinue, nil
		}
	})
	if err != nil {
		return nil, fmt.Errorf("locate preview metadata: %w", err)
	}
	var result []text.Segment
	for _, block := range blocks {
		literal := false
		for _, segment := range protected {
			if block.Start >= segment.Start && block.Start < segment.Stop {
				literal = true
				break
			}
		}
		if literal {
			continue
		}
		result = append(result, block)
	}
	return result, nil
}
