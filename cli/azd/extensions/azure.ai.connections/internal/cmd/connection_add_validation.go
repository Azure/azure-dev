// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"azure.ai.connections/internal/definition"
	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry"
)

type connectionTemplateAnalysis struct {
	required   map[string]struct{}
	referenced map[string]struct{}
	fields     map[string]bool
}

func validateConnectionDefinitionForAuthoring(
	input *definition.Definition,
) ([]string, []string, error) {
	if input == nil {
		return nil, nil, connectionDefinitionError(
			"connection definition must be an object",
			"provide a YAML or JSON object containing the Connection fields",
		)
	}

	if strings.TrimSpace(normalizeKind(input.Category)) == "" {
		return nil, nil, exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"Connection field category must be a non-blank string.",
			"Set category to a non-blank value and retry.",
		)
	}

	analysis := connectionTemplateAnalysis{
		required:   map[string]struct{}{},
		referenced: map[string]struct{}{},
		fields:     map[string]bool{},
	}
	if err := analysis.add("target", input.Target); err != nil {
		return nil, nil, err
	}
	if !analysis.fields["target"] && strings.TrimSpace(input.Target) == "" {
		return nil, nil, exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"Connection field target must be a non-blank string.",
			"Set target to a non-blank value and retry.",
		)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"audience", input.Audience},
		{"authorizationUrl", input.AuthorizationURL},
		{"tokenUrl", input.TokenURL},
		{"refreshUrl", input.RefreshURL},
		{"connectorName", input.ConnectorName},
	} {
		if err := analysis.add(field.name, field.value); err != nil {
			return nil, nil, err
		}
	}
	for index, value := range input.Scopes {
		if err := analysis.add(fmt.Sprintf("scopes[%d]", index), value); err != nil {
			return nil, nil, err
		}
	}
	for _, key := range slices.Sorted(maps.Keys(input.Metadata)) {
		if err := analysis.add("metadata."+key, input.Metadata[key]); err != nil {
			return nil, nil, err
		}
	}
	if err := analyzeConnectionCredentialTemplates(input.Credentials, "credentials", &analysis); err != nil {
		return nil, nil, err
	}

	authType := normalizeAuthType(input.AuthType)
	if authType == "" {
		authType = "None"
	}
	authType = normalizeAuthTypeToARM(authType)
	if authType == "" {
		authType = "None"
	}
	if !isSupportedConnectionAuthType(authType) {
		return nil, nil, exterrors.Validation(
			exterrors.CodeInvalidAuthType,
			"Invalid connection authType.",
			"Set authType to a supported value from the connection service schema.",
		)
	}

	if err := validateConnectionAudienceForAuthoring(input, authType, analysis); err != nil {
		return nil, nil, err
	}
	if err := validateConnectionAuthForAuthoring(input, authType, analysis); err != nil {
		return nil, nil, err
	}

	variables := slices.Sorted(maps.Keys(analysis.required))
	if variables == nil {
		variables = []string{}
	}
	referenced := slices.Sorted(maps.Keys(analysis.referenced))
	if referenced == nil {
		referenced = []string{}
	}
	return variables, referenced, nil
}

func (a *connectionTemplateAnalysis) add(path, value string) error {
	parsed, err := foundry.AnalyzeEnvReferences(value)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("Connection field %q contains an invalid environment template.", path),
			"Correct the environment template syntax and retry.",
		)
	}
	a.fields[path] = parsed.HasClientTemplate
	for _, variable := range parsed.RequiredVariables {
		a.required[variable] = struct{}{}
	}
	for _, variable := range parsed.ReferencedVariables {
		a.referenced[variable] = struct{}{}
	}
	return nil
}

func analyzeConnectionCredentialTemplates(
	value any,
	path string,
	analysis *connectionTemplateAnalysis,
) error {
	switch typed := value.(type) {
	case string:
		return analysis.add(path, typed)
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			if err := analyzeConnectionCredentialTemplates(
				typed[key],
				path+"."+key,
				analysis,
			); err != nil {
				return err
			}
		}
	case rawCredentials:
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			if err := analyzeConnectionCredentialTemplates(
				typed[key],
				path+"."+key,
				analysis,
			); err != nil {
				return err
			}
		}
	case map[string]string:
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			if err := analysis.add(path+"."+key, typed[key]); err != nil {
				return err
			}
		}
	case []any:
		for index, item := range typed {
			if err := analyzeConnectionCredentialTemplates(
				item,
				fmt.Sprintf("%s[%d]", path, index),
				analysis,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func isSupportedConnectionAuthType(authType string) bool {
	switch authType {
	case "AAD", "AccessKey", "AccountKey", "AgenticIdentity", "AgenticIdentityToken",
		"ApiKey", "CustomKeys", "ManagedIdentity", "None", "OAuth2", "PAT",
		"ProjectManagedIdentity", "SAS", "ServicePrincipal", "UserEntraToken",
		"UsernamePassword":
		return true
	default:
		return false
	}
}

func validateConnectionAudienceForAuthoring(
	input *definition.Definition,
	authType string,
	analysis connectionTemplateAnalysis,
) error {
	if input.Audience == "" || analysis.fields["audience"] {
		return nil
	}
	switch authType {
	case "ProjectManagedIdentity", "UserEntraToken", "AgenticIdentityToken", "AgenticIdentity":
		return nil
	default:
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"audience is only valid with ProjectManagedIdentity, UserEntraToken, or AgenticIdentityToken authType.",
			"Remove audience or select one of these identity auth types.",
		)
	}
}

func validateConnectionAuthForAuthoring(
	input *definition.Definition,
	authType string,
	analysis connectionTemplateAnalysis,
) error {
	oauthFields := []struct {
		name  string
		value string
	}{
		{"authorizationUrl", input.AuthorizationURL},
		{"tokenUrl", input.TokenURL},
		{"refreshUrl", input.RefreshURL},
		{"connectorName", input.ConnectorName},
	}
	if authType != "OAuth2" {
		for _, field := range oauthFields {
			if field.value != "" && !analysis.fields[field.name] {
				return exterrors.Validation(
					exterrors.CodeConflictingArguments,
					"authorizationUrl, tokenUrl, refreshUrl, scopes, and connectorName are only valid with OAuth2 authType.",
					"Remove the OAuth2 fields or set authType to OAuth2.",
				)
			}
		}
		if len(input.Scopes) > 0 {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"authorizationUrl, tokenUrl, refreshUrl, scopes, and connectorName are only valid with OAuth2 authType.",
				"Remove the OAuth2 fields or set authType to OAuth2.",
			)
		}
	}

	switch authType {
	case "ApiKey":
		return validateConnectionAPIKeyForAuthoring(input, analysis)
	case "CustomKeys":
		return validateConnectionCustomKeysForAuthoring(input, analysis)
	case "OAuth2":
		return validateConnectionOAuthForAuthoring(input, analysis)
	default:
		return nil
	}
}

func validateConnectionAPIKeyForAuthoring(
	input *definition.Definition,
	analysis connectionTemplateAnalysis,
) error {
	value, found := input.Credentials["key"]
	if !found {
		return exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"ApiKey auth requires a non-blank string credentials.key.",
			"Set credentials.key to a non-blank API key and retry.",
		)
	}
	text, ok := value.(string)
	if !ok || (!analysis.fields["credentials.key"] && strings.TrimSpace(text) == "") {
		return exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"ApiKey auth requires a non-blank string credentials.key.",
			"Set credentials.key to a non-blank API key and retry.",
		)
	}
	return nil
}

func validateConnectionCustomKeysForAuthoring(
	input *definition.Definition,
	analysis connectionTemplateAnalysis,
) error {
	credentials := input.Credentials
	valid := connectionCustomCredentialMapIsValid(credentials, "credentials", analysis)
	if keys, found := credentials["keys"]; found {
		switch typed := keys.(type) {
		case map[string]any:
			valid = connectionCustomCredentialMapIsValid(typed, "credentials.keys", analysis)
		case map[string]string:
			valid = connectionCustomStringMapIsValid(typed, "credentials.keys", analysis)
		case rawCredentials:
			valid = connectionCustomCredentialMapIsValid(
				map[string]any(typed),
				"credentials.keys",
				analysis,
			)
		default:
		}
		if _, flat := keys.(string); flat {
			valid = connectionCustomCredentialMapIsValid(credentials, "credentials", analysis)
		}
	}
	if !valid {
		return exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"CustomKeys auth requires nonempty credentials or credentials.keys with non-blank string keys and values.",
			"Provide at least one named custom credential with a non-blank string value; remove empty entries.",
		)
	}
	return nil
}

func connectionCustomCredentialMapIsValid(
	values map[string]any,
	path string,
	analysis connectionTemplateAnalysis,
) bool {
	if len(values) == 0 {
		return false
	}
	for key, value := range values {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(key) == "" ||
			(!analysis.fields[path+"."+key] && strings.TrimSpace(text) == "") {
			return false
		}
	}
	return true
}

func connectionCustomStringMapIsValid(
	values map[string]string,
	path string,
	analysis connectionTemplateAnalysis,
) bool {
	if len(values) == 0 {
		return false
	}
	for key, value := range values {
		if strings.TrimSpace(key) == "" ||
			(!analysis.fields[path+"."+key] && strings.TrimSpace(value) == "") {
			return false
		}
	}
	return true
}

func validateConnectionOAuthForAuthoring(
	input *definition.Definition,
	analysis connectionTemplateAnalysis,
) error {
	credentials := input.Credentials
	connectorHasTemplate := analysis.fields["connectorName"]
	connectorIsSet := input.ConnectorName != ""
	if connectorIsSet && !connectorHasTemplate && strings.TrimSpace(input.ConnectorName) == "" {
		return exterrors.Validation(
			exterrors.CodeMissingConnectionField,
			"OAuth2 connectorName must be a non-blank string.",
			"Set connectorName for a managed connector, or omit it and provide the BYO OAuth2 fields.",
		)
	}

	urlFields := []struct {
		name  string
		value string
	}{
		{"authorizationUrl", input.AuthorizationURL},
		{"tokenUrl", input.TokenURL},
		{"refreshUrl", input.RefreshURL},
	}
	hasLiteralURL := false
	for _, field := range urlFields {
		if field.value != "" && !analysis.fields[field.name] {
			hasLiteralURL = true
		}
	}
	if connectorIsSet && !connectorHasTemplate {
		if len(credentials) > 0 || len(input.Scopes) > 0 || hasLiteralURL {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"connectorName cannot be combined with authorizationUrl, tokenUrl, refreshUrl, scopes, or credentials.",
				"Use connectorName alone with empty credentials for managed OAuth2, or omit it for BYO OAuth2.",
			)
		}
		return nil
	}

	byoPossible := oauthBYOFieldsPossible(input, analysis)
	if !connectorHasTemplate {
		if !byoPossible {
			return missingOAuthBYOFields()
		}
		return nil
	}

	managedPossible := len(credentials) == 0 && len(input.Scopes) == 0 && !hasLiteralURL
	if !managedPossible && !byoPossible {
		return missingOAuthBYOFields()
	}
	return nil
}

func oauthBYOFieldsPossible(
	input *definition.Definition,
	analysis connectionTemplateAnalysis,
) bool {
	for _, field := range []struct {
		name  string
		value any
	}{
		{"authorizationUrl", input.AuthorizationURL},
		{"tokenUrl", input.TokenURL},
		{"credentials.clientId", input.Credentials["clientId"]},
		{"credentials.clientSecret", input.Credentials["clientSecret"]},
	} {
		text, ok := field.value.(string)
		if !ok {
			return false
		}
		if !analysis.fields[field.name] && strings.TrimSpace(text) == "" {
			return false
		}
	}
	return true
}

func missingOAuthBYOFields() error {
	return exterrors.Validation(
		exterrors.CodeMissingConnectionField,
		"OAuth2 auth requires either connectorName or authorizationUrl, tokenUrl, credentials.clientId, and credentials.clientSecret.",
		"Set connectorName for managed OAuth2, or provide all four BYO OAuth2 fields; refreshUrl and scopes are optional.",
	)
}

func connectionDefinitionError(message, suggestion string) error {
	return exterrors.Validation(exterrors.CodeInvalidParameter, message, suggestion)
}
