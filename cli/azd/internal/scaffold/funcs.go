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
			if separatorStart == -1 {
				separatorStart = i
			}
		default:
			if !isAsciiAlphaNumeric(name[i]) {
				continue
			}
			char := name[i]
			if separatorStart != -1 {
				if separatorStart == 0 {
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

// Provide a reasonable limit to avoid name length issues
const containerAppNameMaxLen = 12

// ContainerAppName returns a name that is valid to be used as an infix for a container app resource.
//
// The name is treated to only contain alphanumeric and dash characters, with no repeated dashes, and no dashes
// as the first or last character.
func ContainerAppName(name string) string {
	if len(name) > containerAppNameMaxLen {
		name = name[:containerAppNameMaxLen]
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
