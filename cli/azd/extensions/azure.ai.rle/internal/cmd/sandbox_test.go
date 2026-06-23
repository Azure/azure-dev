// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "testing"

func TestRetryableSandboxCreateError(t *testing.T) {
	err := &rleHTTPError{
		statusCode: 409,
		body:       `{"error":"Environment 'env' version '1' does not have a ready disk image (conversion status: Pending)."}`,
	}

	message, retry := retryableSandboxCreateError(err)
	if !retry {
		t.Fatal("expected pending conversion error to be retryable")
	}
	if message == "" {
		t.Fatal("expected retry message")
	}
}

func TestFailedSandboxCreateErrorIsNotRetryable(t *testing.T) {
	err := &rleHTTPError{
		statusCode: 409,
		body:       `{"error":"Environment 'env' version '1' does not have a ready disk image (conversion status: Failed)."}`,
	}

	_, retry := retryableSandboxCreateError(err)
	if retry {
		t.Fatal("expected failed conversion error not to be retryable")
	}
}
