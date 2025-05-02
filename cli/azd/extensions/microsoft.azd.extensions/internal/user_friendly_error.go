// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import "fmt"

// UserFriendlyError represents an error with additional user-friendly details.
// This error type is designed to separate technical error messages (which may be displayed
// in red) from instructional content (which should be displayed in normal text).
type UserFriendlyError struct {
	// Technical error message that will be shown as an error
	ErrorMessage string

	// User-friendly additional details or instructions that should be shown in normal text
	UserDetails string
}

// Error returns the technical error message, implementing the error interface
func (e *UserFriendlyError) Error() string {
	return e.ErrorMessage
}

// GetUserDetails returns the user-friendly additional details
func (e *UserFriendlyError) GetUserDetails() string {
	return e.UserDetails
}

// NewUserFriendlyError creates a new UserFriendlyError with the given error message and user details
func NewUserFriendlyError(errorMessage, userDetails string) *UserFriendlyError {
	return &UserFriendlyError{
		ErrorMessage: errorMessage,
		UserDetails:  userDetails,
	}
}

// NewUserFriendlyErrorf creates a new UserFriendlyError with a formatted error message and user details
func NewUserFriendlyErrorf(errorMessage string, userDetails string, args ...interface{}) *UserFriendlyError {
	return &UserFriendlyError{
		ErrorMessage: errorMessage,
		UserDetails:  fmt.Sprintf(userDetails, args...),
	}
}
