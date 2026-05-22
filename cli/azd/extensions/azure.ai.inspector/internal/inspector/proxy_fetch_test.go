// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"strings"
	"testing"
)

func TestReadLimitedBodyRejectsOversizedResponse(t *testing.T) {
	body, err := readLimitedBody(strings.NewReader("abcd"), 4)
	if err != nil {
		t.Fatalf("readLimitedBody returned error: %v", err)
	}
	if string(body) != "abcd" {
		t.Fatalf("body = %q, want abcd", string(body))
	}

	if _, err := readLimitedBody(strings.NewReader("abcde"), 4); err == nil {
		t.Fatal("readLimitedBody should reject a response larger than the limit")
	}
}

func TestReadTruncatedBody(t *testing.T) {
	body, truncated, err := readTruncatedBody(strings.NewReader("abcde"), 4)
	if err != nil {
		t.Fatalf("readTruncatedBody returned error: %v", err)
	}
	if !truncated {
		t.Fatal("readTruncatedBody should report truncation")
	}
	if string(body) != "abcd" {
		t.Fatalf("body = %q, want abcd", string(body))
	}
}
