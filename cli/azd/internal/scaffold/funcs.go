package scaffold

import "strings"

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
// 1. Start with max initial length of 32 characters from the Container App name (https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules#microsoftapp)
// 2. Prefix abbreviation of 'ca-' from abbreviations.json (4 characters)
// 3. Bicep resource token (13 characters) + separator '-' (1 character) -- total of 14 characters
//
// Which leaves us with: 32 - 4 - 14 = 14 characters.
// We allow 2 additional characters for wiggle-room. We've seen failures when container app name is exactly at 32.
const containerAppNameInfixMaxLen = 12

// ContainerAppName returns a name that is valid to be used as an infix for a container app resource.
//
// The name is treated to only contain alphanumeric and dash characters, with no repeated dashes, and no dashes
// as the first or last character.
func ContainerAppName(name string) string {
	if len(name) > containerAppNameInfixMaxLen {
		name = name[:containerAppNameInfixMaxLen]
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
