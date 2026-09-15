// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"azure.ai.connections/internal/exterrors"
)

// validateConnectionProperties validates resolved ARM properties before upsert.
// Call after file-reference and environment expansion. It performs no I/O and
// never normalizes or mutates properties, including nested credentials.
func validateConnectionProperties(props rawConnectionProperties) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"category", props.Category},
		{"target", props.Target},
	} {
		if !nonBlankConnectionString(field.value) {
			return exterrors.Validation(
				exterrors.CodeMissingConnectionField,
				"Connection field "+field.name+" must be a non-blank string.",
				"Set "+field.name+" to a non-blank value and retry.",
			)
		}
	}

	// Accept the complete service schema, not just the create command's subset.
	// ManagedIdentity is distinct from ProjectManagedIdentity. AgenticIdentity
	// is the schema's legacy alias for AgenticIdentityToken.
	switch props.AuthType {
	case "AAD", "AccessKey", "AccountKey", "AgenticIdentity", "AgenticIdentityToken",
		"ApiKey", "CustomKeys", "ManagedIdentity", "None", "OAuth2", "PAT",
		"ProjectManagedIdentity", "SAS", "ServicePrincipal", "UserEntraToken", "UsernamePassword":
	default:
		return exterrors.Validation(
			exterrors.CodeInvalidAuthType,
			"Invalid connection authType.",
			"Set authType to a supported value from the connection service schema, such as None or ApiKey.",
		)
	}

	hasOAuthFields := props.AuthorizationURL != "" || props.TokenURL != "" ||
		props.RefreshURL != "" || len(props.Scopes) > 0
	if props.AuthType != "OAuth2" && (hasOAuthFields || props.ConnectorName != "") {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"authorizationUrl, tokenUrl, refreshUrl, scopes, and connectorName are only valid with OAuth2 authType.",
			"Remove the OAuth2 fields or set authType to OAuth2.",
		)
	}
	if props.Audience != "" {
		switch props.AuthType {
		case "ProjectManagedIdentity", "UserEntraToken", "AgenticIdentityToken", "AgenticIdentity":
		default:
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"audience is only valid with ProjectManagedIdentity, UserEntraToken, or AgenticIdentityToken authType.",
				"Remove audience or select one of these identity auth types.",
			)
		}
	}

	var credentials rawCredentials
	if props.Credentials != nil {
		credentials = *props.Credentials
	}
	switch props.AuthType {
	case "ApiKey":
		if !nonBlankConnectionString(credentials["key"]) {
			return exterrors.Validation(
				exterrors.CodeMissingConnectionField,
				"ApiKey auth requires a non-blank string credentials.key.",
				"Set credentials.key to a non-blank API key and retry.",
			)
		}
	case "CustomKeys":
		// Preserve the legacy flat form, including a string-valued key named
		// "keys". An object-valued "keys" selects the nested ARM form instead.
		valid := nonBlankConnectionKeys(credentials)
		if keys, found := credentials["keys"]; found {
			switch keys := keys.(type) {
			case map[string]any:
				valid = nonBlankConnectionKeys(keys)
			case map[string]string:
				valid = nonBlankConnectionKeys(keys)
			case rawCredentials:
				valid = nonBlankConnectionKeys(keys)
			}
		}
		if !valid {
			return exterrors.Validation(
				exterrors.CodeMissingConnectionField,
				"CustomKeys auth requires nonempty credentials or credentials.keys with non-blank string keys and values.",
				"Provide at least one named custom credential with a non-blank string value; remove empty entries.",
			)
		}
	case "OAuth2":
		_, hasClientID := credentials["clientId"]
		_, hasClientSecret := credentials["clientSecret"]
		hasBYO := hasOAuthFields || hasClientID || hasClientSecret
		if props.ConnectorName != "" {
			if !nonBlankConnectionString(props.ConnectorName) {
				return exterrors.Validation(
					exterrors.CodeMissingConnectionField,
					"OAuth2 connectorName must be a non-blank string.",
					"Set connectorName for a managed connector, or omit it and provide the BYO OAuth2 fields.",
				)
			}
			if hasBYO || len(credentials) > 0 {
				return exterrors.Validation(
					exterrors.CodeConflictingArguments,
					"connectorName cannot be combined with authorizationUrl, tokenUrl, refreshUrl, scopes, "+
						"credentials.clientId, credentials.clientSecret, or other credential fields.",
					"Use connectorName alone with empty credentials for managed OAuth2, or omit it for BYO OAuth2.",
				)
			}
			return nil
		}
		if !hasBYO {
			return exterrors.Validation(
				exterrors.CodeMissingConnectionField,
				"OAuth2 auth requires either connectorName or authorizationUrl, tokenUrl, "+
					"credentials.clientId, and credentials.clientSecret.",
				"Set connectorName for managed OAuth2, or provide all four BYO OAuth2 fields; "+
					"refreshUrl and scopes are optional.",
			)
		}
		var missing []string
		for _, field := range []struct {
			name  string
			value any
		}{
			{"authorizationUrl", props.AuthorizationURL},
			{"tokenUrl", props.TokenURL},
			{"credentials.clientId", credentials["clientId"]},
			{"credentials.clientSecret", credentials["clientSecret"]},
		} {
			if !nonBlankConnectionString(field.value) {
				missing = append(missing, field.name)
			}
		}
		if len(missing) > 0 {
			return exterrors.Validation(
				exterrors.CodeMissingConnectionField,
				"BYO OAuth2 has missing or invalid fields: "+strings.Join(missing, ", ")+".",
				"Set each required field to a non-blank string; refreshUrl and scopes are optional.",
			)
		}
	}
	return nil
}

func nonBlankConnectionString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

func nonBlankConnectionKeys[V any](keys map[string]V) bool {
	if len(keys) == 0 {
		return false
	}
	for key, value := range keys {
		if !nonBlankConnectionString(key) || !nonBlankConnectionString(value) {
			return false
		}
	}
	return true
}
