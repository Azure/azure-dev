// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validation

// ValidationResult represents the result of intent validation
type ValidationResult struct {
	Status      ValidationStatus
	Explanation string
	Confidence  float64
}

// ValidationStatus represents the completion status of the original intent
type ValidationStatus string

const (
	ValidationComplete   ValidationStatus = "COMPLETE"
	ValidationPartial    ValidationStatus = "PARTIAL"
	ValidationIncomplete ValidationStatus = "INCOMPLETE"
	ValidationError      ValidationStatus = "ERROR"
)
