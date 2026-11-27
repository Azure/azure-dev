// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"errors"
	"net/url"
	"regexp"
)

var credentialURLPattern = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s"<>]+`)

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

// NonDisclosureError redacts URL credentials, query parameters, and fragments from errors.
func NonDisclosureError(err error) error {
	return nonDisclosureError(err)
}

type redactedSentinelError struct {
	message  string
	sentinel error
}

func (e *redactedSentinelError) Error() string { return e.message }

func (e *redactedSentinelError) Unwrap() error { return e.sentinel }

func nonDisclosureError(err error) error {
	if err == nil {
		return nil
	}
	sanitized := redactURLs(err.Error())
	switch {
	case errors.Is(err, context.Canceled):
		return &redactedSentinelError{message: sanitized, sentinel: context.Canceled}
	case errors.Is(err, context.DeadlineExceeded):
		return &redactedSentinelError{message: sanitized, sentinel: context.DeadlineExceeded}
	default:
		return errors.New(sanitized)
	}
}
