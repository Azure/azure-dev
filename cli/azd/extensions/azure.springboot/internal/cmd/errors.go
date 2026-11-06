// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

const (
	codeConflictingArguments     = "conflicting_arguments"
	codeConsentRequired          = "consent_required"
	codeInvalidProfiles          = "invalid_profiles"
	codeInvalidConfiguration     = "invalid_configuration"
	codeUnsupportedConfiguration = "unsupported_configuration"
	codeUnresolvedDecision       = "unresolved_decision"
	codeCancelled                = "cancelled"
)

func validationError(code, message, suggestion string) error {
	return &azdext.LocalError{
		Code: code, Category: azdext.LocalErrorCategoryValidation,
		Message: message, Suggestion: suggestion,
	}
}
