// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"crypto/sha256"
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

	allUpper := isAllUpperCase(name)

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
			var char byte
			if separatorStart == 0 || i == 0 { // we are at the start
				char = lowerCase(name[i])
				separatorStart = -1
			} else if separatorStart > 0 { // end of separator, and it's not the first one
				char = upperCase(name[i])
				separatorStart = -1
			} else if allUpper { // when the input is all uppercase, convert to lowercase
				char = lowerCase(name[i])
			} else {
				char = name[i]
			}

			sb.WriteByte(char)
		}
	}

	return sb.String()
}

// BicepNameInfix is like BicepName, except that the first character is upper-cased for infix use.
func BicepNameInfix(name string) string {
	bicepName := BicepName(name)
	return capitalizeFirst(bicepName)
}

func capitalizeFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func RemoveDotAndDash(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, ".", ""), "-", "")
}

// EntryEnvKey returns an environment variable name scoped to a resource entry.
func EntryEnvKey(entry, key string) string {
	// Preserve repeated/trailing hyphens: collapsing them would merge distinct entry names.
	entry = strings.ReplaceAll(strings.ToUpper(entry), "-", "_")
	return "AZURE_RESOURCES_" + entry + "__" + strings.TrimPrefix(key, "AZURE_")
}

// AlphaSnakeUpper returns a name in upper-snake case alphanumeric name separated only by underscores.
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

// AlphaSnakeUpperFromCasing transforms a camel case or pascal case name into an upper-snake case name.
func AlphaSnakeUpperFromCasing(name string) string {
	result := strings.Builder{}
	for i := range name {
		c := name[i]
		if 'A' <= c && c <= 'Z' {
			if i > 0 && i != len(name)-1 {
				result.WriteRune('_')
			}
		}

		result.WriteByte(upperCase(c))
	}

	return result.String()
}

func isAllUpperCase(c string) bool {
	for i := range c {
		if 'a' <= c[i] && c[i] <= 'z' {
			return false
		}
	}

	return true
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

// 32 characters are allowed for the Container App name. See
// https://learn.microsoft.com/azure/azure-resource-manager/management/resource-name-rules#microsoftapp
//
// We allow 2 additional characters for wiggle-room. We've seen failures when container app name is exactly at 32.
const containerAppNameMaxLen = 30

var containerAppNameSeparatorsRegex = regexp.MustCompile(`[-_]+`)
var containerAppNameInvalidCharsRegex = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

func containerAppName(name string, maxLen int) string {
	if len(name) > maxLen {
		name = name[:maxLen]
	}

	// Normalize separators before filtering to preserve existing generated names.
	name = containerAppNameSeparatorsRegex.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	name = containerAppNameInvalidCharsRegex.ReplaceAllString(name, "")
	return strings.ToLower(name)
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
	return fmt.Sprintf("${%s}", AzureSnakeCase(src))
}

func AzureSnakeCase(src string) string {
	return fmt.Sprintf(
		"AZURE_%s", strings.ReplaceAll(strings.ToUpper(camelCaseRegex.ReplaceAllString(src, "${1}_${2}")), "-", "_"))
}

func HasACA(services []ServiceSpec) bool {
	return hasHostType(services, ContainerAppKind)
}

func HasAppService(services []ServiceSpec) bool {
	return hasHostType(services, AppServiceKind)
}

func IsACA(host HostKind) bool {
	return host == ContainerAppKind
}

func IsAppService(host HostKind) bool {
	return host == AppServiceKind
}

func hasHostType(services []ServiceSpec, host HostKind) bool {
	for _, service := range services {
		if service.Host == host {
			return true
		}
	}
	return false
}

// Formats a parameter value for use in a bicep file.
// If the value is a string, it is quoted inline with no indentation.
// Otherwise, the value is marshaled with indentation specified by prefix and indent.
func FormatParameter(prefix string, indent string, value any) (string, error) {
	if valueStr, ok := value.(string); ok {
		value = valueStr
	}

	val, err := json.MarshalIndent(value, prefix, indent)
	if err != nil {
		return "", err
	}
	return string(val), nil
}

// BicepString escapes untrusted text for insertion inside a single-quoted Bicep string.
func BicepString(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"'", `\u{27}`,
		"${", "\\${",
		"\r", "\\r",
		"\n", "\\n",
	)
	return replacer.Replace(value)
}

// SecretName returns a host-local secret identifier for an environment variable.
func SecretName(environmentName string) string {
	// Environment names may end in underscores or exceed the secret name limit.
	// A fixed-size digest preserves distinct names without inheriting those restrictions.
	return fmt.Sprintf("user-%x", sha256.Sum256([]byte(environmentName)))
}
