// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SourceLocations indexes property positions without putting authored names or values in diagnostics.
// Multiple locations are retained because dotted authoring paths can be ambiguous.
func SourceLocations(data []byte) (map[string][]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	locations := map[string][]string{}
	cursor, line, column := int64(0), 1, 1
	position := func(offset int64) string {
		for cursor < offset {
			value, size := utf8.DecodeRune(data[cursor:offset])
			if value == '\n' {
				line, column = line+1, 1
			} else {
				column++
			}
			cursor += int64(size)
		}
		return fmt.Sprintf("Line %d, column %d", line, column)
	}
	nextOffset := func() int64 {
		offset := decoder.InputOffset()
		for offset < int64(len(data)) && strings.ContainsRune(" \t\r\n,:", rune(data[offset])) {
			offset++
		}
		return offset
	}
	var visit func(string, int64) error
	visit = func(path string, offset int64) error {
		locations[path] = append(locations[path], position(offset))
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			for decoder.More() {
				offset := nextOffset()
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("invalid schema property token")
				}
				child := name
				if path != "$" {
					child = path + "." + name
				}
				if err := visit(child, offset); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		case json.Delim('['):
			for index := 0; decoder.More(); index++ {
				if err := visit(path+"."+strconv.Itoa(index), nextOffset()); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		}
		return err
	}
	if err := visit("$", nextOffset()); err != nil {
		return nil, err
	}
	return locations, nil
}

// DiagnosticLocation falls back to the containing object for missing properties.
func (f Finding) DiagnosticLocation(locations map[string][]string) string {
	for path := f.Path; path != ""; {
		if positions := locations[path]; len(positions) != 0 {
			return strings.Join(positions, "; ")
		}
		index := strings.LastIndexByte(path, '.')
		if index < 0 {
			break
		}
		path = path[:index]
	}
	return f.DiagnosticPath()
}
