// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var httpURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// RedactURL strips userinfo, query parameters, and fragments from rawURL so
// embedded credentials, SAS signatures, and tokens are never surfaced.
func RedactURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return rawURL
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func redactURLFromText(text, rawURL string) string {
	if rawURL == "" {
		return text
	}
	redacted := RedactURL(rawURL)
	if redacted == rawURL {
		return text
	}
	text = strings.ReplaceAll(text, rawURL, redacted)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return text
	}
	if parsed.User != nil {
		if password, ok := parsed.User.Password(); ok && password != "" {
			text = strings.ReplaceAll(text, password, "[redacted]")
		}
		if username := parsed.User.Username(); username != "" {
			text = strings.ReplaceAll(text, username, "[redacted]")
		}
	}
	for _, values := range parsed.Query() {
		for _, value := range values {
			if len(value) >= 8 {
				text = strings.ReplaceAll(text, value, "[redacted]")
			}
		}
	}
	if len(parsed.Fragment) >= 8 {
		text = strings.ReplaceAll(text, parsed.Fragment, "[redacted]")
	}
	return text
}

func redactNestedErrorURLs(text string, err error, depth int) string {
	if err == nil || depth > 64 {
		return text
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		text = redactURLFromText(text, urlErr.URL)
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, nested := range multi.Unwrap() {
			text = redactNestedErrorURLs(text, nested, depth+1)
		}
		return text
	}
	return redactNestedErrorURLs(text, errors.Unwrap(err), depth+1)
}

func safeErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	text := redactNestedErrorURLs(err.Error(), err, 0)
	matches := httpURLPattern.FindAllString(text, -1)
	for _, match := range matches {
		trimmed := strings.TrimRight(match, ".,;:)]}")
		text = redactURLFromText(text, trimmed)
	}
	return text
}
