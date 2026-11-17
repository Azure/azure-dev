// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"bufio"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
)

func parseSpringProperties(data string) ([]map[string]string, error) {
	documents := []map[string]string{{}}
	scanner := bufio.NewScanner(strings.NewReader(strings.TrimPrefix(data, "\ufeff")))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var continued string
	continuing := false
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if !continuing && (line == "#---" || line == "!---") {
			documents = append(documents, map[string]string{})
			continue
		}
		line = strings.TrimLeft(line, " \t\f")
		if !continuing && (strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!")) {
			continue
		}
		line = continued + line
		slashes := 0
		for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
			slashes++
		}
		if slashes%2 != 0 {
			continued = line[:len(line)-1]
			continuing = true
			continue
		}
		continued = ""
		continuing = false
		if err := parsePropertyLine(line, documents[len(documents)-1]); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("Spring properties exceed the supported line size")
	}
	if continuing {
		if err := parsePropertyLine(continued, documents[len(documents)-1]); err != nil {
			return nil, err
		}
	}
	return documents, nil
}

func parsePropertyLine(line string, properties map[string]string) error {
	if line == "" {
		return nil
	}
	end, escaped := len(line), false
	for i := range len(line) {
		if !escaped && strings.ContainsRune(" \t\f=:", rune(line[i])) {
			end = i
			break
		}
		if line[i] == '\\' {
			escaped = !escaped
		} else {
			escaped = false
		}
	}
	key, err := unescapeProperty(line[:end])
	if err != nil {
		return err
	}
	value := strings.TrimLeft(line[end:], " \t\f")
	if strings.HasPrefix(value, "=") || strings.HasPrefix(value, ":") {
		value = strings.TrimLeft(value[1:], " \t\f")
	}
	decoded, err := unescapeProperty(value)
	if err != nil {
		return err
	}
	properties[key] = decoded
	return nil
}

func unescapeProperty(value string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out.WriteByte(value[i])
			continue
		}
		i++
		if i == len(value) {
			break
		}
		switch value[i] {
		case 't':
			out.WriteByte('\t')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 'f':
			out.WriteByte('\f')
		case 'u':
			if i+4 >= len(value) {
				return "", errors.New("invalid Unicode escape in Spring properties")
			}
			code, err := strconv.ParseUint(value[i+1:i+5], 16, 16)
			if err != nil {
				return "", errors.New("invalid Unicode escape in Spring properties")
			}
			i += 4
			r := rune(code)
			if r >= 0xd800 && r <= 0xdbff {
				if i+6 >= len(value) || value[i+1:i+3] != `\u` {
					return "", errors.New("invalid Unicode surrogate in Spring properties")
				}
				low, err := strconv.ParseUint(value[i+3:i+7], 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return "", errors.New("invalid Unicode surrogate in Spring properties")
				}
				r = utf16.DecodeRune(r, rune(low))
				i += 6
			} else if r >= 0xdc00 && r <= 0xdfff {
				return "", errors.New("invalid Unicode surrogate in Spring properties")
			}
			out.WriteRune(r)
		default:
			out.WriteByte(value[i])
		}
	}
	return out.String(), nil
}
