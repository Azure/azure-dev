// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// TestNotFoundOnALaterPageIsNotAMissingAsset covers the case that makes a
// broken listing dangerous rather than merely wrong.
//
// The reconciler asks "is this evaluator published?" by listing its versions
// and reading a 404 as no. If the 404 came from page two, the first page had
// already answered yes -- and treating the break as "nothing published" has
// the reconciler publish, writing over a rubric its owner did not change, and
// report success.
func TestNotFoundOnALaterPageIsNotAMissingAsset(t *testing.T) {
	notFound := &azcore.ResponseError{StatusCode: http.StatusNotFound}

	if !IsNotFound(notFound) {
		t.Fatal("a plain 404 stopped reading as not-found")
	}
	if IsNotFound(pageWalkError{cause: notFound}) {
		t.Error("a 404 raised part-way through a page walk read as a missing asset")
	}
}

// TestAWalkFailureStillClassifiesAsItself keeps the wrapper from swallowing
// the reason. A cancelled context or an expired token during a walk has to
// stay reportable as what it is, or the reader is told a listing was truncated
// when they actually need to sign in again.
func TestAWalkFailureStillClassifiesAsItself(t *testing.T) {
	cause := &azcore.ResponseError{StatusCode: http.StatusUnauthorized}
	wrapped := error(pageWalkError{cause: cause})

	respErr, ok := errors.AsType[*azcore.ResponseError](wrapped)
	if !ok {
		t.Fatal("the cause is no longer reachable through the wrapper")
	}
	if respErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("unwrapped to %d, want %d", respErr.StatusCode, http.StatusUnauthorized)
	}
	if IsConflict(wrapped) {
		t.Error("a 401 during a walk read as a conflict")
	}
}

// TestWalkFailureSaysWhereItHappened is for the reader looking at the message.
// "404" on its own sends them looking for a missing evaluator; the wrapper has
// to say the listing broke while continuing.
func TestWalkFailureSaysWhereItHappened(t *testing.T) {
	err := error(pageWalkError{cause: errors.New("connection reset")})
	const want = "later page"
	if got := err.Error(); !strings.Contains(got, want) {
		t.Errorf("message %q does not mention %q", got, want)
	}
}
