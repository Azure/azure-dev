// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package urlsafe renders URLs for logs and errors without their credentials.
//
// It exists because url.URL.Redacted looks like the safe choice and is not: it
// masks a userinfo password only, and leaves the query string untouched. A
// storage SAS carries its credential in the query as sig, so logging a SAS URI
// with Redacted writes a live credential to disk. A token supplied as
// username-only userinfo survives it too, because only the password is masked.
package urlsafe

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

const urlStartPattern = `(?i)(?:\bhttps?:[/\\]*|\b[a-z][a-z0-9+.-]*:[/\\]{1,2}|[/\\]{2})`

var (
	embeddedURL = regexp.MustCompile(urlStartPattern + `[^\s]+`)
	urlStart    = regexp.MustCompile(urlStartPattern)
)

// Text removes URL credentials from prose such as a service error message.
// A malformed URL is hidden entirely rather than risking a partial redaction.
func Text(text string) string {
	return embeddedURL.ReplaceAllStringFunc(text, func(raw string) string {
		// Adjacent URLs can be parsed as one URL whose path contains another
		// authority. Hide the ambiguous token instead of leaking its userinfo.
		if len(urlStart.FindAllStringIndex(raw, 2)) > 1 {
			return "<redacted-url>"
		}
		drivePath := len(raw) >= 3 && raw[1] == ':' && (raw[2] == '\\' || raw[2] == '/')
		if (drivePath || strings.HasPrefix(raw, `\\`)) && !strings.ContainsAny(raw, "@?#") {
			return raw
		}
		// Quotes can be valid inside userinfo or a query. Only peel trailing
		// prose punctuation, never split a credential-bearing URL at a quote.
		candidate := strings.TrimRight(raw, `"'.,;)}>`)
		suffix := strings.TrimPrefix(raw, candidate)
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Host == "" {
			return "<redacted-url>" + suffix
		}
		return URL(parsed) + suffix
	})
}

// URL renders a URL with its credentials, query and fragment removed, keeping
// the scheme, host and path so the log still says where the request went.
func URL(u *url.URL) string {
	if u == nil {
		return ""
	}
	safe := *u
	// Dropped whole rather than left to Redacted, which masks the password and
	// prints the username as given -- and a bare token is a username.
	safe.User = nil
	safe.RawQuery = ""
	safe.Fragment = ""
	return safe.String()
}

// Error rebuilds a *url.Error without its request URL. http.Client.Do embeds
// the full URL in the error text, so a DNS, TLS, timeout or cancellation
// failure on a SAS-backed request would otherwise show the credential to the
// user. The original error is left unmodified.
func Error(err error) error {
	urlError, ok := errors.AsType[*url.Error](err)
	if !ok {
		return err
	}
	safe := "<redacted>"
	if u, parseErr := url.Parse(urlError.URL); parseErr == nil {
		safe = URL(u)
	}
	return &url.Error{Op: urlError.Op, URL: safe, Err: urlError.Err}
}
