// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultilingualPreview(t *testing.T) {
	const identifier = "RECORD0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for _, sample := range []struct {
		language, heading, city, greeting string
	}{
		{"zh-TW", "城市", "台北", "你好，世界"},
		{"ja", "都市", "東京", "こんにちは"},
		{"ko", "도시", "서울", "안녕하세요"},
		{"fr", "Ville", "Québec", "Bonjour"},
		{"de", "Stadt", "München", "Grüße"},
		{"hi", "शहर", "दिल्ली", "नमस्ते"},
		{"ar", "المدينة", "القاهرة", "مرحبا"},
		{"graphemes", "地點👩🏽‍💻", "Cafe\u0301 🇹🇼", "👩🏽‍💻"},
	} {
		t.Run(sample.language, func(t *testing.T) {
			source := "# " + sample.language + "\n\n**" + sample.greeting + "**\n\n" +
				"<table>\n<tr><th>" + sample.heading + "</th><th>Description</th></tr>\n" +
				"<tr><td>" + sample.city + "</td><td>" + identifier + "</td></tr>\n</table>\n"
			for _, width := range []int{20, 40, 80, 120} {
				for _, color := range []bool{false, true} {
					t.Run(fmt.Sprintf("width=%d/color=%t", width, color), func(t *testing.T) {
						var output bytes.Buffer
						require.NoError(t, Render(t.Context(), &output, source, width, color))
						plain := ansi.Strip(output.String())
						assert.Contains(t, plain, sample.heading)
						assert.Contains(t, plain, sample.city)
						assert.Contains(t, plain, sample.greeting)
						assert.Contains(t, plain, identifier)
						assert.NotContains(t, plain, "\ufffd")
						assert.NotContains(t, plain, "[HTML table not rendered:")
						if !color {
							assert.NotContains(t, output.String(), "\x1b")
						}
						if width <= 40 {
							assert.Contains(t, plain, "Row 1")
							positions := map[string]int{}
							for line := range strings.SplitSeq(plain, "\n") {
								for _, value := range []string{sample.city, identifier} {
									if prefix, ok := strings.CutSuffix(line, value); ok {
										positions[value] = ansi.StringWidth(prefix)
									}
								}
							}
							require.Len(t, positions, 2)
							expected := ansi.StringWidth(sample.heading) + 1 +
								max(len(sample.heading), len("Description")) - len(sample.heading) + 2
							assert.Equal(t, expected, positions[sample.city])
							assert.Equal(t, max(len(sample.heading), len("Description"))+3, positions[identifier])
							t.Logf("pinned cards: localized value column=%d, ASCII value column=%d",
								positions[sample.city], positions[identifier])
						} else {
							assert.NotContains(t, plain, "Row 1")
						}
					})
				}
			}
			t.Run("table-column-measurements", func(t *testing.T) {
				source := "<table>\n<tr><th>" + sample.heading + "</th><th>Status</th></tr>\n" +
					"<tr><td>" + sample.city + "</td><td>READY</td></tr>\n" +
					"<tr><td>" + sample.greeting + "</td><td>DONE</td></tr>\n</table>\n"
				plain := renderPreview(t, source, 120)
				columns := map[string]int{}
				for line := range strings.SplitSeq(plain, "\n") {
					for _, suffix := range []string{"Status", "READY", "DONE"} {
						if prefix, ok := strings.CutSuffix(line, suffix); ok {
							columns[suffix] = ansi.StringWidth(prefix)
						}
					}
				}
				require.Len(t, columns, 3)
				if sample.language == "hi" {
					assert.Equal(t, map[string]int{"Status": 8, "READY": 5, "DONE": 7}, columns,
						"characterization: pinned azd rune widths differ from ANSI grapheme widths for these Indic strings")
					t.Logf("known Hindi table-alignment limitation, zero-based ANSI columns: %v", columns)
					return
				}
				expected := max(ansi.StringWidth(sample.heading), ansi.StringWidth(sample.city),
					ansi.StringWidth(sample.greeting)) + 3
				for name, column := range columns {
					assert.Equal(t, expected, column, sample.language+"/"+name)
				}
			})
		})
	}
}
