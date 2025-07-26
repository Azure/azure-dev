// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validation

import (
	"context"
	"fmt"

	"github.com/tmc/langchaingo/llms"

	"azd.ai.start/internal/session"
	"azd.ai.start/internal/utils"
)

// IntentValidator validates whether the original intent was fulfilled
type IntentValidator struct {
	llm llms.Model
}

// NewIntentValidator creates a new intent validator
func NewIntentValidator(llm llms.Model) *IntentValidator {
	return &IntentValidator{llm: llm}
}

// ValidateCompletion validates whether the original intent was fulfilled
func (iv *IntentValidator) ValidateCompletion(
	originalIntent string,
	executedActions []session.ActionLog,
) *ValidationResult {
	if len(executedActions) == 0 {
		return &ValidationResult{
			Status:      ValidationIncomplete,
			Explanation: "No actions were executed",
			Confidence:  1.0,
		}
	}

	validationPrompt := fmt.Sprintf(`
Original User Intent: %s

Actions Executed:
%s

Based on the original intent and the actions that were executed, evaluate whether the user's intent was fulfilled.

Respond with one of: COMPLETE, PARTIAL, INCOMPLETE, ERROR

Then provide a brief explanation of your assessment.

Format your response as:
STATUS: [COMPLETE/PARTIAL/INCOMPLETE/ERROR]
EXPLANATION: [Your explanation]
CONFIDENCE: [0.0-1.0]`,
		originalIntent,
		utils.FormatActionsForValidation(executedActions))

	result, err := iv.llm.Call(context.Background(), validationPrompt)
	if err != nil {
		return &ValidationResult{
			Status:      ValidationError,
			Explanation: fmt.Sprintf("Validation failed: %s", err.Error()),
			Confidence:  0.0,
		}
	}

	return ParseValidationResult(result)
}
