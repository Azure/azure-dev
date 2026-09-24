// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package urlsafe

import (
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sasSecret = "REDACT_ME_SECRET"

// These tests pin the premise as well as the behavior: url.URL.Redacted is the
// call that looks correct and leaks, so if someone reaches for it again the
// first assertion explains why they should not.
func TestURLDropsTheSASSignature(t *testing.T) {
	raw := "https://acct.blob.core.windows.net/c/rows.jsonl?sv=2021-08-06&sig=" + sasSecret
	u, err := url.Parse(raw)
	require.NoError(t, err)

	assert.Contains(t, u.Redacted(), sasSecret,
		"guards the premise: Redacted() alone leaks the signature")

	safe := URL(u)
	assert.NotContains(t, safe, sasSecret, "the SAS signature must never reach a log")
	assert.NotContains(t, safe, "sig=")
	assert.Equal(t, "https://acct.blob.core.windows.net/c/rows.jsonl", safe,
		"scheme, host and path stay, so the log still says where the request went")
	assert.Equal(t, raw, u.String(), "the caller's URL is untouched and still usable")
}

func TestURLHandlesNil(t *testing.T) {
	assert.Equal(t, "", URL(nil))
}

// Redacted() masks a userinfo password and prints the username verbatim, so a
// credential passed either way has to be dropped here rather than rendered.
func TestURLDropsUserinfoCredentials(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"password", "https://user:" + sasSecret + "@acct.blob.core.windows.net/c/rows.jsonl"},
		{"token as username", "https://" + sasSecret + "@acct.blob.core.windows.net/c/rows.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.raw)
			require.NoError(t, err)

			safe := URL(u)
			assert.NotContains(t, safe, sasSecret, "userinfo must never reach a log")
			assert.NotContains(t, safe, "@")
			assert.Equal(t, "https://acct.blob.core.windows.net/c/rows.jsonl", safe)
			assert.Equal(t, tc.raw, u.String(), "the caller's URL is untouched")
		})
	}

	u, err := url.Parse("https://" + sasSecret + "@acct.blob.core.windows.net/c/rows.jsonl")
	require.NoError(t, err)
	assert.Contains(t, u.Redacted(), sasSecret,
		"guards the premise: Redacted() alone leaks a username-only token")
}

// The transport error carries the request URL, so userinfo has to be dropped on
// that path too and not only on the query.
func TestErrorStripsUserinfoFromTransportFailures(t *testing.T) {
	original := &url.Error{
		Op:  "Get",
		URL: "https://" + sasSecret + "@acct.blob.core.windows.net/c/rows.jsonl",
		Err: errors.New("dial tcp: lookup failed"),
	}

	got := Error(original)

	assert.NotContains(t, got.Error(), sasSecret)
	assert.Contains(t, got.Error(), "acct.blob.core.windows.net")
}

func TestErrorStripsTheSASFromTransportFailures(t *testing.T) {
	inner := errors.New("dial tcp: lookup failed")
	original := &url.Error{
		Op:  "Get",
		URL: "https://acct.blob.core.windows.net/c/rows.jsonl?sig=" + sasSecret,
		Err: inner,
	}

	got := Error(original)

	assert.NotContains(t, got.Error(), sasSecret,
		"a transport failure must not show the SAS to the user")
	assert.Contains(t, got.Error(), "acct.blob.core.windows.net",
		"the host stays so the message still says where it failed")
	assert.ErrorIs(t, got, inner, "the cause stays unwrappable")
	assert.Contains(t, original.URL, sasSecret, "the original error is not mutated")
}

func TestErrorLeavesOtherErrorsAlone(t *testing.T) {
	plain := errors.New("some other failure")
	assert.Same(t, plain, Error(plain))
	assert.Nil(t, Error(nil))
}

func TestTextRedactsEmbeddedURLCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, message, expected string
	}{
		{
			"userinfo query and fragment",
			`Download "https://user-secret:password-secret@storage.example/rows.jsonl` +
				`?sig=signature-secret#fragment-secret" failed.`,
			`Download "https://storage.example/rows.jsonl" failed.`,
		},
		{
			"username token",
			"Request to https://username-secret@service.example/run failed.",
			"Request to https://service.example/run failed.",
		},
		{
			"multiple URLs",
			"Read https://first.example/rows?sig=first-secret then https://second.example/error#second-secret",
			"Read https://first.example/rows then https://second.example/error",
		},
		{
			"quote inside credentials",
			"Failed 'https://user-secret:pass'word-secret@host/file?sig=signature-secret'.",
			"Failed 'https://host/file'.",
		},
		{
			"quote inside query",
			"Failed https://host/file?sig='signature-secret'.",
			"Failed https://host/file'.",
		},
		{
			"parenthesized URL",
			"Failed (https://user-secret:password-secret@host/file?sig=signature-secret).",
			"Failed (https://host/file).",
		},
		{
			"malformed escape",
			"Could not read https://user-secret:password-secret@host/%invalid?sig=signature-secret",
			"Could not read <redacted-url>",
		},
		{
			"IPv6",
			"Failed: https://user-secret:password-secret@[::1]:443/file?sig=signature-secret#fragment-secret",
			"Failed: https://[::1]:443/file",
		},
		{
			"protocol relative",
			"Failed: //user-secret:password-secret@host/file?sig=signature-secret#fragment-secret",
			"Failed: //host/file",
		},
		{
			"service URI",
			"Result azureai://user-secret:password-secret@accounts/example?sig=signature-secret#fragment-secret",
			"Result azureai://accounts/example",
		},
		{
			"identifier prefix",
			"Failed url_https:/user-secret:password-secret@host/file?sig=signature-secret#fragment-secret",
			"Failed url_<redacted-url>",
		},
		{
			"assignment prefix",
			"Failed url=https://user-secret:password-secret@host/file?sig=signature-secret#fragment-secret",
			"Failed url=https://host/file",
		},
		{
			"malformed assignment prefix",
			"Failed url=https:/user-secret:password-secret@host/file?sig=signature-secret#fragment-secret",
			"Failed url=<redacted-url>",
		},
		{
			"punctuation prefix",
			"Failed (url:https:/user-secret:password-secret@host/file?sig=signature-secret#fragment-secret).",
			"Failed (url:<redacted-url>).",
		},
		{"without URLs", "The evaluator could not initialize.", "The evaluator could not initialize."},
		{"safe URL", "Request https://service.example/run failed.", "Request https://service.example/run failed."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			safe := Text(tc.message)
			assert.Equal(t, tc.expected, safe)
			assert.NotContains(t, safe, "-secret")
			assert.NotContains(t, safe, "sig=")
		})
	}
}

func TestTextRedactsAdjacentURLs(t *testing.T) {
	for _, text := range []string{
		`{"primary":"https://safe.example/a","secondary":"https://fixture-user:fixture-password@private.example/b"}`,
		"https://safe.example/a,https://fixture-user:fixture-password@private.example/b",
		"https://safe.example/a,//fixture-user:fixture-password@private.example/b",
	} {
		safe := Text(text)
		assert.Contains(t, safe, "<redacted-url>")
		assert.NotContains(t, safe, "fixture-user")
		assert.NotContains(t, safe, "fixture-password")
	}
}

func TestTextRedactsMalformedSchemeURLs(t *testing.T) {
	for _, malformed := range []string{
		"https:fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"http:fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"https:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"http:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"HtTpS:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"azureai:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		`https:\fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment`,
		"https:///fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		`\\fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment`,
		`/\fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment`,
		`C:\fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment`,
	} {
		for _, message := range []string{
			"Failed " + malformed,
			"https://safe.example/a," + malformed,
			malformed + ",https://safe.example/a",
			`{"primary":"https://safe.example/a","secondary":"` + malformed + `"}`,
		} {
			t.Run(message, func(t *testing.T) {
				safe := Text(message)
				assert.Contains(t, safe, "<redacted-url>")
				for _, secret := range []string{
					"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment",
				} {
					assert.NotContains(t, safe, secret)
				}
			})
		}
	}

}

func TestTextRedactsHTTPURLsAfterIdentifiers(t *testing.T) {
	for _, prefix := range []string{"url_", "value7", "field", "caf\u00e9", "url=", "url:", "url,", "url("} {
		for _, scheme := range []string{"https:", "http:/", "HtTpS:/", `https:\`, "https://", "https:///"} {
			raw := prefix + scheme + "fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment"
			for _, message := range []string{
				"Failed " + raw,
				"https://safe.example/path," + raw,
			} {
				t.Run(message, func(t *testing.T) {
					safe := Text(message)
					for _, secret := range []string{
						"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment", "sig=",
					} {
						assert.NotContains(t, safe, secret)
					}
				})
			}
		}
	}
}

func TestTextPreservesCredentialFreeDiagnosticContext(t *testing.T) {
	for _, message := range []string{
		`Cannot open C:\data\rows.jsonl`,
		`Cannot open c:/data/rows.jsonl`,
		`Cannot open \\server\share\rows.jsonl`,
		"Evaluation failed: retry after checking the dataset.",
		"Could not initialize https://service.example/evals/run",
		"Could not read //storage.example/data/rows.jsonl",
		"Check url_https and fieldhttp settings.",
		"Failed url_https://service.example/run",
	} {
		assert.Equal(t, message, Text(message))
	}
}
