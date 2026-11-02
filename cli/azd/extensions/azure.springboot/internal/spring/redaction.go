// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"net/url"
	"regexp"
)

var credentialURLPattern = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<redacted-url>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func redactURLs(text string) string {
	return credentialURLPattern.ReplaceAllStringFunc(text, redactURL)
}

func nonDisclosureError(err error) error {
	if err == nil || err == context.Canceled || err == context.DeadlineExceeded {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return &sanitizedContextError{message: redactURLs(err.Error()), cause: context.Canceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &sanitizedContextError{message: redactURLs(err.Error()), cause: context.DeadlineExceeded}
	}
	return errors.New(redactURLs(err.Error()))
}

type sanitizedContextError struct {
	message string
	cause   error
}

func (e *sanitizedContextError) Error() string {
	return e.message
}

func (e *sanitizedContextError) Unwrap() error {
	return e.cause
}
