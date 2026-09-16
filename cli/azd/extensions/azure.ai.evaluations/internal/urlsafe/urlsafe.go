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
)

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
