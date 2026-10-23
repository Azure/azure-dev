// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"azure.ai.content-understanding/internal/cuclient"

	"go.yaml.in/yaml/v3"
)

type span struct {
	Offset int `json:"offset"`
	Length int `json:"length"`
}

type page struct {
	Number *int   `json:"pageNumber"`
	Spans  []span `json:"spans"`
}

type segment struct {
	ID       string `json:"segmentId"`
	Category string `json:"category"`
	Start    *int   `json:"startPageNumber"`
	End      *int   `json:"endPageNumber"`
	Span     *span  `json:"span"`
}

type llmContent struct {
	Kind      string                     `json:"kind"`
	MIME      string                     `json:"mimeType"`
	Metadata  json.RawMessage            `json:"metadata"`
	Category  string                     `json:"category"`
	Path      string                     `json:"path"`
	Markdown  string                     `json:"markdown"`
	Start     *int                       `json:"startPageNumber"`
	End       *int                       `json:"endPageNumber"`
	StartTime *int64                     `json:"startTimeMs"`
	EndTime   *int64                     `json:"endTimeMs"`
	Pages     []page                     `json:"pages"`
	Segments  []segment                  `json:"segments"`
	Fields    map[string]json.RawMessage `json:"fields"`
}

type llmWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Target  string `json:"target"`
}

func invalidLLM() error {
	return &cuclient.Error{
		Kind: cuclient.ErrorService, Code: "invalid_llm_result",
		Message:    "The analysis result cannot be rendered as LLM input.",
		Suggestion: "Use --llm-input=false to inspect the complete JSON service response.",
	}
}

// RenderLLM follows the Python SDK's default to_llm_input contract.
// YAML whitespace may differ; data types, ordered front matter, and content boundaries do not.
func RenderLLM(raw json.RawMessage) (string, error) {
	var result struct {
		Contents []llmContent `json:"contents"`
		Warnings []llmWarning `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Contents) == 0 {
		return "", invalidLLM()
	}
	contents := renderableContents(result.Contents)
	avCount := 0
	for _, content := range contents {
		if content.Kind == "audioVisual" {
			avCount++
		}
	}
	var blocks []string
	for _, content := range contents {
		mapping := &yaml.Node{Kind: yaml.MappingNode}
		mime := content.MIME
		if mime == "" {
			mime = "unknown"
		}
		appendYAML(mapping, "mimeType", mime)
		if len(content.Metadata) != 0 && string(content.Metadata) != "null" {
			metadata, err := decodeValue(content.Metadata)
			if err != nil {
				return "", invalidLLM()
			}
			appendYAML(mapping, "metadata", metadata)
		}
		if content.Kind == "audioVisual" && avCount > 1 && content.StartTime != nil && content.EndTime != nil {
			value := formatTime(*content.StartTime) + " \u2013 " + formatTime(*content.EndTime)
			appendYAML(mapping, "timeRange", value)
		}
		if content.Category != "" {
			appendYAML(mapping, "category", content.Category)
		}
		if content.Kind == "document" {
			if pages := formatPages(content); pages != nil {
				appendYAML(mapping, "pages", pages)
			}
		}
		fields, err := resolveFields(content.Fields)
		if err != nil {
			return "", invalidLLM()
		}
		if len(fields) != 0 {
			appendYAML(mapping, "fields", fields)
		}
		warnings := &yaml.Node{Kind: yaml.SequenceNode}
		for _, warning := range result.Warnings {
			if strings.HasPrefix(strings.TrimSpace(warning.Message), "LLMStats:") {
				continue
			}
			entry := &yaml.Node{Kind: yaml.MappingNode}
			for _, pair := range [][2]string{
				{"code", warning.Code}, {"message", warning.Message}, {"target", warning.Target},
			} {
				if pair[1] != "" {
					appendYAML(entry, pair[0], pair[1])
				}
			}
			if len(entry.Content) != 0 {
				warnings.Content = append(warnings.Content, entry)
			}
		}
		if len(warnings.Content) != 0 {
			mapping.Content = append(mapping.Content, yamlValue("warnings"), warnings)
		}
		var encoded bytes.Buffer
		encoder := yaml.NewEncoder(&encoded)
		encoder.SetIndent(2)
		if err := encoder.Encode(mapping); err != nil {
			return "", invalidLLM()
		}
		if err := encoder.Close(); err != nil {
			return "", invalidLLM()
		}
		block := "---\n" + strings.TrimSuffix(encoded.String(), "\n") + "\n---"
		if content.Markdown != "" {
			markdown := content.Markdown
			if content.Kind == "document" {
				markdown = pageMarkers(content)
			}
			block += "\n" + markdown
		}
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return "", invalidLLM()
	}
	return strings.Join(blocks, "\n\n*****\n\n"), nil
}

func renderableContents(contents []llmContent) []llmContent {
	routed := map[string]bool{}
	for _, content := range contents {
		if content.Kind == "document" && content.Category != "" && content.Path != "" {
			routed[content.Path] = true
		}
	}
	var result []llmContent
	expanded := false
	for _, content := range contents {
		if content.Kind != "document" || content.Category != "" || len(content.Segments) == 0 {
			result = append(result, content)
			continue
		}
		expanded = true
		for _, segment := range content.Segments {
			if segment.ID != "" && routed[content.Path+"/"+segment.ID] {
				continue
			}
			markdown := ""
			if segment.Span != nil {
				runes := []rune(content.Markdown)
				start := min(max(segment.Span.Offset, 0), len(runes))
				end := min(max(segment.Span.Offset+segment.Span.Length, start), len(runes))
				markdown = string(runes[start:end])
			}
			result = append(result, llmContent{
				Kind: "document", MIME: content.MIME, Markdown: markdown,
				Category: segment.Category, Start: segment.Start, End: segment.End,
			})
		}
	}
	if expanded {
		slices.SortStableFunc(result, func(left, right llmContent) int {
			start := func(content llmContent) int {
				if content.Kind == "document" && content.Start != nil {
					return *content.Start
				}
				return 0
			}
			return cmp.Compare(start(left), start(right))
		})
	}
	return result
}

func decodeValue(raw json.RawMessage) (any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&value)
	return value, err
}

func resolveFields(fields map[string]json.RawMessage) (map[string]any, error) {
	result := map[string]any{}
	for name, raw := range fields {
		value, err := resolveField(raw)
		if err != nil {
			return nil, err
		}
		if value != nil {
			result[name] = value
		}
	}
	return result, nil
}

func resolveField(raw json.RawMessage) (any, error) {
	var field map[string]json.RawMessage
	if err := json.Unmarshal(raw, &field); err != nil || field == nil {
		return nil, invalidLLM()
	}
	var kind string
	if err := json.Unmarshal(field["type"], &kind); err != nil {
		return nil, err
	}
	key := ""
	switch kind {
	case "object":
		var children map[string]json.RawMessage
		if len(field["valueObject"]) == 0 {
			return nil, nil
		}
		if err := json.Unmarshal(field["valueObject"], &children); err != nil {
			return nil, err
		}
		value, err := resolveFields(children)
		if err != nil || len(value) == 0 {
			return nil, err
		}
		return value, nil
	case "array":
		var children []json.RawMessage
		if len(field["valueArray"]) == 0 {
			return nil, nil
		}
		if err := json.Unmarshal(field["valueArray"], &children); err != nil {
			return nil, err
		}
		var value []any
		for _, child := range children {
			item, err := resolveField(child)
			if err != nil {
				return nil, err
			}
			if item != nil {
				value = append(value, item)
			}
		}
		if len(value) == 0 {
			return nil, nil
		}
		return value, nil
	case "string", "date", "time", "integer", "number", "boolean", "json":
		key = "value" + strings.ToUpper(kind[:1]) + kind[1:]
	default:
		return nil, invalidLLM()
	}
	if len(field[key]) == 0 {
		return nil, nil
	}
	return decodeValue(field[key])
}

func appendYAML(mapping *yaml.Node, key string, value any) {
	mapping.Content = append(mapping.Content, yamlValue(key), yamlValue(value))
}

func yamlValue(value any) *yaml.Node {
	switch value := value.(type) {
	case map[string]any:
		node := &yaml.Node{Kind: yaml.MappingNode}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			appendYAML(node, key, value[key])
		}
		return node
	case []any:
		node := &yaml.Node{Kind: yaml.SequenceNode}
		for _, item := range value {
			node.Content = append(node.Content, yamlValue(item))
		}
		return node
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(string(value), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: string(value)}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value)}
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	default:
		panic("unhandled internal YAML value")
	}
}

func formatTime(ms int64) string {
	seconds := ms / 1000
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func formatPages(content llmContent) any {
	var numbers []int
	for _, page := range content.Pages {
		if page.Number != nil {
			numbers = append(numbers, *page.Number)
		}
	}
	if len(numbers) == 0 {
		if content.Start == nil || content.End == nil {
			return nil
		}
		if *content.Start == *content.End {
			return *content.Start
		}
		return fmt.Sprintf("%d-%d", *content.Start, *content.End)
	}
	slices.Sort(numbers)
	if len(numbers) == 1 {
		return numbers[0]
	}
	var ranges []string
	first, last := numbers[0], numbers[0]
	addRange := func() {
		text := strconv.Itoa(first)
		if first != last {
			text += "-" + strconv.Itoa(last)
		}
		ranges = append(ranges, text)
	}
	for _, number := range numbers[1:] {
		if number == last+1 {
			last = number
		} else {
			addRange()
			first, last = number, number
		}
	}
	addRange()
	return strings.Join(ranges, ", ")
}

var pageBreak = regexp.MustCompile(`\n*<!-- PageBreak -->\n*`)

func pageMarkers(content llmContent) string {
	if strings.Contains(content.Markdown, "<!-- InputPageNumber:") {
		return content.Markdown
	}
	type marker struct{ offset, number int }
	var markers []marker
	for _, page := range content.Pages {
		if page.Number != nil && len(page.Spans) != 0 {
			markers = append(markers, marker{page.Spans[0].Offset, *page.Number})
		}
	}
	if len(markers) == 0 {
		start := 1
		if content.Start != nil {
			start = *content.Start
		}
		var chunks []string
		for index, chunk := range pageBreak.Split(content.Markdown, -1) {
			if text := strings.TrimSpace(chunk); text != "" {
				chunks = append(chunks, fmt.Sprintf("<!-- InputPageNumber: %d -->\n\n%s", start+index, text))
			}
		}
		return strings.Join(chunks, "\n\n")
	}
	slices.SortStableFunc(markers, func(left, right marker) int { return cmp.Compare(left.offset, right.offset) })
	breaks := pageBreak.FindAllStringIndex(content.Markdown, -1)
	cleaned := []rune(pageBreak.ReplaceAllString(content.Markdown, "\n\n"))
	var output strings.Builder
	previous := 0
	for _, marker := range markers {
		offset := marker.offset
		for _, found := range breaks {
			start := len([]rune(content.Markdown[:found[0]]))
			if marker.offset > start {
				offset -= len([]rune(content.Markdown[found[0]:found[1]])) - 2
			}
		}
		offset = min(max(offset, previous), len(cleaned))
		output.WriteString(string(cleaned[previous:offset]))
		fmt.Fprintf(&output, "<!-- InputPageNumber: %d -->\n\n", marker.number)
		previous = offset
	}
	output.WriteString(string(cleaned[previous:]))
	return output.String()
}
