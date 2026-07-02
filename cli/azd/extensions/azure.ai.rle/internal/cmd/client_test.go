// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestServiceErrorSuggestionShowsCurrentEndpoint(t *testing.T) {
	t.Setenv("RLE_ENDPOINT", "https://rle.example.test")

	err := serviceError(errors.New("dial tcp failed"))
	serviceErr, ok := err.(*azdext.ServiceError)
	if !ok {
		t.Fatalf("expected ServiceError, got %T", err)
	}

	for _, expected := range []string{
		"Ensure the RLE control plane is running and reachable.",
		"Trying at https://rle.example.test;",
		"RLE_ENDPOINT=<endpoint>",
	} {
		if !strings.Contains(serviceErr.Suggestion, expected) {
			t.Fatalf("expected suggestion to contain %q, got %q", expected, serviceErr.Suggestion)
		}
	}
}
