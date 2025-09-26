// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
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
		return fmt.Sprintf("\"%s\"", valueStr), nil
	}

	val, err := json.MarshalIndent(value, prefix, indent)
	if err != nil {
		return "", err
	}
	return string(val), nil
}

func hostFromEndpoint(endpoint string) (string, error) {
	urlEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint: %s", endpoint)
	}

	return urlEndpoint.Hostname(), nil
}

func aiProjectEndpoint(endpoints string) (string, error) {
	result := gjson.Parse(endpoints)
	if !result.Exists() {
		return "", fmt.Errorf("invalid endpoints JSON: %s", endpoints)
	}

	endpoint := result.Get("AI Foundry API")
	if !endpoint.Exists() {
		return "", fmt.Errorf("endpoint 'AI Foundry API' not found in endpoints")
	}

	return endpoint.String(), nil
}

func emitAiProjectEndpoint(projectEndpointsVar string) (string, error) {
	return fmt.Sprintf("%s['AI Foundry API']", projectEndpointsVar), nil
}

func emitHostFromEndpoint(endpointVar string) (string, error) {
	// example: https://{your-namespace}.servicebus.windows.net:443
	return fmt.Sprintf("split(split(%s, '//')[1], ':')[0]", endpointVar), nil

}

func bicepFuncCall(funcName string) func(name string) string {
	// example: toLower(foo)
	return func(name string) string {
		return fmt.Sprintf("%s(%s)", funcName, name)
	}
}

func bicepFuncCallThree(funcName string) func(a string, b string, c string) string {
	// example: replace(foo, bar, baz)
	return func(a string, b string, c string) string {
		return fmt.Sprintf("%s(%s, %s, %s)", funcName, a, b, c)
	}
}
