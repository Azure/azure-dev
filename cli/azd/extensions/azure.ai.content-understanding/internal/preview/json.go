// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	terminalColor "github.com/fatih/color"
)

// RenderJSON preserves JSON values and numeric precision while styling terminal output.
func RenderJSON(ctx context.Context, writer io.Writer, source []byte, color bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, source, "", "  "); err != nil {
		return fmt.Errorf("format JSON preview: %w", err)
	}
	formatted.WriteByte('\n')
	var safe bytes.Buffer
	for _, r := range formatted.String() {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Bidi_Control, r) {
			fmt.Fprintf(&safe, "\\u%04x", r)
		} else {
			safe.WriteRune(r)
		}
	}
	var styled bytes.Buffer
	if color && os.Getenv("NO_COLOR") == "" {
		lexer := lexers.Get("json")
		if lexer == nil {
			return fmt.Errorf("JSON preview lexer is unavailable")
		}
		tokens, err := lexer.Tokenise(nil, safe.String())
		if err != nil {
			return fmt.Errorf("style JSON preview: %w", err)
		}
		keyColor := terminalColor.New(terminalColor.FgBlue)
		stringColor := terminalColor.New(terminalColor.FgGreen)
		nullColor := terminalColor.New(terminalColor.FgHiBlack)
		keyColor.EnableColor()
		stringColor.EnableColor()
		nullColor.EnableColor()
		for token := tokens(); token != chroma.EOF; token = tokens() {
			value := token.Value
			switch token.Type {
			case chroma.NameTag:
				value = keyColor.Sprint(value)
			case chroma.LiteralStringDouble:
				value = stringColor.Sprint(value)
			case chroma.KeywordConstant:
				if value == "null" {
					value = nullColor.Sprint(value)
				}
			}
			styled.WriteString(value)
		}
	} else {
		styled = safe
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n, err := writer.Write(styled.Bytes()); err != nil {
		return fmt.Errorf("write JSON preview: %w", err)
	} else if n != styled.Len() {
		return fmt.Errorf("write JSON preview: %w", io.ErrShortWrite)
	}
	return nil
}
