package scaffold

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// BicepName returns a name suitable for use as a bicep variable name.
//
// The name is converted to camel case, with treatment for underscore or dash separators,
// and all non-alphanumeric characters are removed.
func BicepName(name string) string {
	sb := strings.Builder{}
	separatorStart := -1
	for i := range name {
		switch name[i] {
		case '-', '_':
			if separatorStart == -1 { // track first occurrence of consecutive separators
				separatorStart = i
			}
		default:
			if !isAsciiAlphaNumeric(name[i]) {
				continue
			}
			char := name[i]
			if separatorStart != -1 {
				if separatorStart == 0 { // first character should be lowerCase
					char = lowerCase(name[i])
				} else {
					char = upperCase(name[i])
				}
				separatorStart = -1
			}

			if i == 0 {
				char = lowerCase(name[i])
			}

			sb.WriteByte(char)
		}
	}

	return sb.String()
}

// UpperSnakeAlpha returns a name in upper-snake case alphanumeric name separated only by underscores.
//
// Non-alphanumeric characters are discarded, while consecutive separators ('-', '_', and '.') are treated
// as a single underscore separator.
func AlphaSnakeUpper(name string) string {
	sb := strings.Builder{}
	separatorStart := -1
	for i := range name {
		switch name[i] {
		case '-', '_', '.':
			if separatorStart == -1 { // track first occurrence of consecutive separators
				separatorStart = i
			}
		default:
			if !isAsciiAlphaNumeric(name[i]) {
				continue
			}

			if separatorStart != -1 {
				if separatorStart != 0 { // don't write prefix separator
					sb.WriteByte('_')
				}
				separatorStart = -1
			}

			sb.WriteByte(upperCase(name[i]))
		}
	}

	return sb.String()
}

func isAsciiAlphaNumeric(c byte) bool {
	return ('0' <= c && c <= '9') || ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z')
}

func upperCase(r byte) byte {
	if 'a' <= r && r <= 'z' {
		r -= 'a' - 'A'
	}
	return r
}

func lowerCase(r byte) byte {
	if 'A' <= r && r <= 'Z' {
		r += 'a' - 'A'
	}
	return r
}

// Provide a reasonable limit for the container app infix to avoid name length issues
// This is calculated as follows:
//  1. Start with max initial length of 32 characters from the Container App name
//     https://learn.microsoft.com/azure/azure-resource-manager/management/resource-name-rules#microsoftapp
//  2. Prefix abbreviation of 'ca-' from abbreviations.json (4 characters)
//  3. Bicep resource token (13 characters) + separator '-' (1 character) -- total of 14 characters
//
// Which leaves us with: 32 - 4 - 14 = 14 characters.
const containerAppNameInfixMaxLen = 12

// We allow 2 additional characters for wiggle-room. We've seen failures when container app name is exactly at 32.
const containerAppNameMaxLen = 30

func containerAppName(name string, maxLen int) string {
	if len(name) > maxLen {
		name = name[:maxLen]
	}

	// trim to allowed characters:
	// - only alphanumeric and '-'
	// - no repeated '-'
	// - no '-' as the first or last character
	sb := strings.Builder{}
	i := 0
	for i < len(name) {
		if isAsciiAlphaNumeric(name[i]) {
			sb.WriteByte(lowerCase(name[i]))
		} else if name[i] == '-' || name[i] == '_' {
			j := i + 1
			for j < len(name) && (name[j] == '-' || name[i] == '_') { // find consecutive matches
				j++
			}

			if i != 0 && j != len(name) { // only write '-' if not first or last character
				sb.WriteByte('-')
			}

			i = j
			continue
		}

		i++
	}

	return sb.String()
}

// ContainerAppName returns a suitable name a container app resource.
//
// The name is treated to only contain alphanumeric and dash characters, with no repeated dashes, and no dashes
// as the first or last character.
func ContainerAppName(name string) string {
	return containerAppName(name, containerAppNameMaxLen)
}

// ContainerAppSecretName returns a suitable name a container app secret name.
//
// The name is treated to only contain lowercase alphanumeric and dash characters, and must start and end with an
// alphanumeric character
func ContainerAppSecretName(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), "_", "-")
}

// camelCaseRegex is a regular expression used to match camel case patterns.
// It matches a lowercase letter or digit followed by an uppercase letter.
var camelCaseRegex = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// EnvFormat takes an input parameter like `fooParam` which is expected to be in camel case and returns it in
// upper snake case with env var template, like `${AZURE_FOO_PARAM}`.
func EnvFormat(src string) string {
	snake := strings.ReplaceAll(strings.ToUpper(camelCaseRegex.ReplaceAllString(src, "${1}_${2}")), "-", "_")
	return fmt.Sprintf("${AZURE_%s}", snake)
}

// ContainerAppInfix returns a suitable infix for a container app resource.
//
// The name is treated to only contain alphanumeric and dash characters, with no repeated dashes, and no dashes
// as the first or last character.
func ContainerAppInfix(name string) string {
	return containerAppName(name, containerAppNameInfixMaxLen)
}

// Formats a parameter value for use in a bicep file.
// If the value is a string, it is quoted inline with no indentation.
// Otherwise, the value is marshaled with indentation specified by prefix and indent.
func FormatParameter(prefix string, indent string, value any) (string, error) {
	if valueStr, ok := value.(string); ok {
		return fmt.Sprintf("\"%s\"", valueStr), nil
	}

	val, err := json.MarshalIndent(value, prefix, indent)
	if err != nil {
		return "", err
	}
	return string(val), nil
}
