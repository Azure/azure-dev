// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validation

import (
	"strings"
)

// ParseValidationResult parses the validation result from LLM response
func ParseValidationResult(response string) *ValidationResult {
	result := &ValidationResult{
		Status:      ValidationError,
		Explanation: "Failed to parse validation response",
		Confidence:  0.0,
	}

	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "STATUS:") {
			statusStr := strings.TrimSpace(strings.TrimPrefix(line, "STATUS:"))
			switch strings.ToUpper(statusStr) {
			case "COMPLETE":
				result.Status = ValidationComplete
			case "PARTIAL":
				result.Status = ValidationPartial
			case "INCOMPLETE":
				result.Status = ValidationIncomplete
			case "ERROR":
				result.Status = ValidationError
			}
		} else if strings.HasPrefix(line, "EXPLANATION:") {
			result.Explanation = strings.TrimSpace(strings.TrimPrefix(line, "EXPLANATION:"))
		} else if strings.HasPrefix(line, "CONFIDENCE:") {
			confidenceStr := strings.TrimSpace(strings.TrimPrefix(line, "CONFIDENCE:"))
			if conf, err := parseFloat(confidenceStr); err == nil {
				result.Confidence = conf
			}
		}
	}

	// If we couldn't parse the status, try to infer from the response content
	if result.Status == ValidationError {
		responseUpper := strings.ToUpper(response)
		if strings.Contains(responseUpper, "COMPLETE") {
			result.Status = ValidationComplete
		} else if strings.Contains(responseUpper, "PARTIAL") {
			result.Status = ValidationPartial
		} else if strings.Contains(responseUpper, "INCOMPLETE") {
			result.Status = ValidationIncomplete
		}
		result.Explanation = response
		result.Confidence = 0.7
	}

	return result
}

// parseFloat safely parses a float from string
func parseFloat(s string) (float64, error) {
	// Simple float parsing for confidence values
	s = strings.TrimSpace(s)
	if s == "1" || s == "1.0" {
		return 1.0, nil
	} else if s == "0" || s == "0.0" {
		return 0.0, nil
	} else if strings.HasPrefix(s, "0.") {
		// Simple decimal parsing for common cases
		switch s {
		case "0.1":
			return 0.1, nil
		case "0.2":
			return 0.2, nil
		case "0.3":
			return 0.3, nil
		case "0.4":
			return 0.4, nil
		case "0.5":
			return 0.5, nil
		case "0.6":
			return 0.6, nil
		case "0.7":
			return 0.7, nil
		case "0.8":
			return 0.8, nil
		case "0.9":
			return 0.9, nil
		}
	}
	return 0.5, nil // Default confidence
}
