// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const redactedURLPlaceholder = "[redacted-url]"

var httpURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

// RedactURL strips userinfo, query parameters, and fragments so
// credentials, SAS signatures, and tokens are never surfaced.
func RedactURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return redactedURLPlaceholder
	}
	if parsed.Opaque != "" {
		if strings.EqualFold(parsed.Scheme, "http") ||
			strings.EqualFold(parsed.Scheme, "https") ||
			strings.ContainsAny(rawURL, "@?#") {
			return redactedURLPlaceholder
		}
		return rawURL
	}
	if (strings.EqualFold(parsed.Scheme, "http") ||
		strings.EqualFold(parsed.Scheme, "https")) && parsed.Host == "" {
		return redactedURLPlaceholder
	}
	if parsed.Scheme == "" && parsed.Host == "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
		return rawURL
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// RedactText redacts embedded HTTP(S) URLs in place without mutating
// surrounding text or later URLs before they are redacted.
func RedactText(text string) string {
	redacted, _ := redactHTTPURLSpans(text)
	return redacted
}

func redactHTTPURLSpans(text string) (string, []string) {
	var redactedURLs []string
	out := httpURLPattern.ReplaceAllStringFunc(text, func(match string) string {
		trimmed := strings.TrimRight(match, `.,;:)]}\`)
		suffix := match[len(trimmed):]
		redacted := RedactURL(trimmed)
		if redacted != trimmed {
			redactedURLs = append(redactedURLs, trimmed)
			return redacted + suffix
		}
		return match
	})
	return out, redactedURLs
}

func scrubURLSecrets(text, rawURL string) string {
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

func collectNestedErrorURLs(err error, depth int, out *[]string) {
	if err == nil || depth > 64 {
		return
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok && urlErr.URL != "" {
		*out = append(*out, urlErr.URL)
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, nested := range multi.Unwrap() {
			collectNestedErrorURLs(nested, depth+1, out)
		}
		return
	}
	collectNestedErrorURLs(errors.Unwrap(err), depth+1, out)
}

func safeErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()

	var nestedURLs []string
	collectNestedErrorURLs(err, 0, &nestedURLs)
	// Sort longest first so a shorter URL never alters a longer one.
	slices.SortFunc(nestedURLs, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	nestedURLs = slices.Compact(nestedURLs)

	var redactedURLs []string
	// Phase 1a: replace Go-quoted nested *url.Error URLs in place.
	for _, rawURL := range nestedURLs {
		redacted := RedactURL(rawURL)
		if redacted == rawURL {
			continue
		}
		text = strings.ReplaceAll(
			text, strconv.Quote(rawURL), strconv.Quote(redacted),
		)
		redactedURLs = append(redactedURLs, rawURL)
	}

	// Phase 1b: redact remaining HTTP(S) URLs strictly by span.
	text, httpURLs := redactHTTPURLSpans(text)
	redactedURLs = append(redactedURLs, httpURLs...)

	// Phase 1c: catch unquoted nested URLs (e.g. relative paths).
	for _, rawURL := range nestedURLs {
		redacted := RedactURL(rawURL)
		if redacted == rawURL {
			continue
		}
		text = strings.ReplaceAll(text, rawURL, redacted)
	}

	// Phase 2: scrub residual secrets echoed outside URLs.
	for _, rawURL := range redactedURLs {
		text = scrubURLSecrets(text, rawURL)
	}
	return text
}
