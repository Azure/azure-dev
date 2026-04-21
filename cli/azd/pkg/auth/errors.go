// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"

	msal "github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
)

// ErrNoCurrentUser indicates that the current user is not logged in.
// This is typically determined by inspecting the stored auth information and credentials on the machine.
// If the auth information or credentials are not found or invalid, the user is considered not to be logged in.
var ErrNoCurrentUser = errors.New("not logged in, run `azd auth login` to login")

// ReLoginRequiredError indicates that the logged in user needs to perform a log in to reauthenticate.
// This typically means that while the credentials stored on the machine are valid, the server has rejected
// the credentials due to expired credentials, or additional challenges being required.
type ReLoginRequiredError struct {
	loginCmd string

	// The scenario in which the login is required
	scenario string

	errText string

	helpLink *errorhandler.ErrorLink
}

// TokenProtectionBlockedError indicates that the token request was blocked by
// an organization's Conditional Access token protection policy (AADSTS530084), and until #7704 is addressed,
// re-running `azd auth login` won't help.
type TokenProtectionBlockedError struct {
	errText string
}

// AuthInteractionError marks AAD-classified, non-retriable errors that carry
// user-facing guidance via *internal.ErrorWithSuggestion. Callers that just need
// to know "this is an actionable auth failure" (e.g., gRPC status mapping or
// suppressing the raw error in `azd auth status` / `--only-check-status` flows)
// should use errors.AsType[AuthInteractionError](err) so future variants don't
// require touching every call site.
//
// The interface is sealed via an unexported marker method; only types declared
// in this package may implement it.
type AuthInteractionError interface {
	error
	isAuthInteractionError()
}

func (*ReLoginRequiredError) isAuthInteractionError()        {}
func (*TokenProtectionBlockedError) isAuthInteractionError() {}

// newActionableAuthError inspects an AAD error response and, if it matches a known
// pattern that azd can surface with actionable guidance, returns a wrapped error and true.
//
// The returned error may be a *ReLoginRequiredError (the user should rerun `azd auth login`)
// or a *TokenProtectionBlockedError (Conditional Access token protection blocked the request
// and re-running `azd auth login` will not help). Callers must not assume the result always
// implies the user can simply log in again — inspect the underlying error type if behavior needs to differ.
//
// If the response does not match a known pattern, it returns (nil, false).
func newActionableAuthError(
	response *AadErrorResponse,
	scopes []string,
	cloud *cloud.Cloud,
	tenantID string,
) (error, bool) {
	if response == nil {
		return nil, false
	}

	if err, ok := newTokenProtectionBlockedError(response, scopes); ok {
		return err, true
	}

	//nolint:lll
	// https://learn.microsoft.com/azure/active-directory/develop/reference-aadsts-error-codes#handling-error-codes-in-your-application
	switch response.Error {
	case "invalid_grant",
		"interaction_required":
		err := ReLoginRequiredError{}
		err.init(response, scopes, cloud, tenantID)
		// Note: Do not prefix with "Suggestion:" here — the UX renderer
		// (ErrorWithSuggestion.ToString) already adds that prefix when displaying.
		suggestion := fmt.Sprintf("%s, run `%s` to acquire a new token.", err.scenario, err.loginCmd)
		suggestionErr := &internal.ErrorWithSuggestion{
			Err:        &err,
			Message:    scenarioMessage(err.scenario),
			Suggestion: suggestion,
		}
		if err.helpLink != nil {
			suggestionErr.Links = []errorhandler.ErrorLink{*err.helpLink}
		}
		return suggestionErr, true
	}

	return nil, false
}

const (
	conditionalAccessDocsLink = "https://aka.ms/TBCADocs"
	// #nosec G101 -- documentation URL, not a credential.
	tokenProtectionFAQLink = "https://aka.ms/TokenProtectionFAQ#troubleshooting"
)

func newTokenProtectionBlockedError(response *AadErrorResponse, scopes []string) (error, bool) {
	if response == nil {
		return nil, false
	}

	if !slices.Contains(response.ErrorCodes, 530084) {
		return nil, false
	}

	message := "A Conditional Access token protection policy blocked this token request."
	if usesGraphScope(scopes) {
		message = "A Conditional Access token protection policy blocked this Microsoft Graph token request."
	}

	return &internal.ErrorWithSuggestion{
		Err: &TokenProtectionBlockedError{
			errText: response.ErrorDescription,
		},
		Message:    message,
		Suggestion: "Contact your IT administrator or request a policy exception.",
		Links: []errorhandler.ErrorLink{
			{
				URL:   conditionalAccessDocsLink,
				Title: "Conditional Access token protection guidance",
			},
			{
				URL:   tokenProtectionFAQLink,
				Title: "Token protection FAQ",
			},
		},
	}, true
}

func usesGraphScope(scopes []string) bool {
	return slices.ContainsFunc(scopes, func(scope string) bool {
		return strings.HasPrefix(scope, "https://graph.microsoft.com/")
	})
}

func scenarioMessage(scenario string) string {
	if scenario == "" {
		return ""
	}

	return strings.ToUpper(scenario[:1]) + scenario[1:] + "."
}

func (e *ReLoginRequiredError) init(
	response *AadErrorResponse,
	scopes []string,
	cloud *cloud.Cloud,
	tenantID string,
) {
	e.errText = response.ErrorDescription
	e.scenario = "reauthentication required"
	e.loginCmd = "azd auth login"

	if tenantID != "" {
		e.loginCmd += fmt.Sprintf(" --tenant-id %s", tenantID)
	}

	loginScopes := LoginScopesFull(cloud)
	for _, scope := range scopes {
		// filter out default login scopes
		if !slices.Contains(loginScopes, scope) {
			e.loginCmd += fmt.Sprintf(" --scope %s", scope)
		}
	}

	// The refresh token has expired, either due to inactivity (700082) or due to
	// sign-in frequency checks enforced by Conditional Access (70043).
	if slices.Contains(response.ErrorCodes, 70043) || slices.Contains(response.ErrorCodes, 700082) {
		e.scenario = "login expired"
	}

	// In a Codespaces environment, `azd auth login` defaults to device code flow, which can cause issues
	// getting tokens if the Entra tenant has Conditional Access Policies set.
	if slices.Contains(response.ErrorCodes, 50005) {
		e.loginCmd += " --use-device-code=false"
		e.helpLink = &errorhandler.ErrorLink{
			URL:   "https://aka.ms/azd/troubleshoot/conditional-access-policy",
			Title: "Conditional Access policy troubleshooting",
		}
	}
}

func (e *ReLoginRequiredError) Error() string {
	return e.errText
}

func (e *TokenProtectionBlockedError) Error() string {
	return e.errText
}

// Marker method to indicate this as non-retriable when executed within an armruntime pipeline
func (e *ReLoginRequiredError) NonRetriable() {
}

// Marker method to indicate this as non-retriable when executed within an armruntime pipeline
func (e *TokenProtectionBlockedError) NonRetriable() {
}

const authFailedPrefix string = "failed to authenticate"

// An error response from Azure Active Directory.
//
// See https://www.rfc-editor.org/rfc/rfc6749#section-5.2 for OAuth 2.0 spec
// See https://learn.microsoft.com/azure/active-directory/develop/reference-aadsts-error-codes for AAD error codes
type AadErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	ErrorCodes       []int  `json:"error_codes"`
	Timestamp        string `json:"timestamp"`
	TraceId          string `json:"trace_id"`
	CorrelationId    string `json:"correlation_id"`
	ErrorUri         string `json:"error_uri"`
}

// AuthFailedError indicates an authentication request has failed.
// This serves as a wrapper around MSAL related errors.
type AuthFailedError struct {
	// The HTTP response motivating the error, if available
	RawResp *http.Response
	// The unmarshaled error response, if available
	Parsed *AadErrorResponse

	innerErr error
}

func newAuthFailedErrorFromMsalErr(err error) error {
	var res *http.Response
	if msalCallErr, ok := errors.AsType[msal.CallErr](err); ok {
		res = msalCallErr.Resp
	} else if authFailedErr, ok := errors.AsType[*AuthFailedError](err); ok { // in case this is re-thrown in a retry loop
		res = authFailedErr.RawResp
	}

	e := &AuthFailedError{RawResp: res, innerErr: err}
	e.parseResponse()
	return e
}

func (e *AuthFailedError) parseResponse() {
	if e.RawResp == nil {
		return
	}

	body, err := io.ReadAll(e.RawResp.Body)
	e.RawResp.Body.Close()
	if err != nil {
		log.Printf("error reading aad response body: %v", err)
		return
	}
	e.RawResp.Body = io.NopCloser(bytes.NewReader(body))

	var er AadErrorResponse
	if err := json.Unmarshal(body, &er); err != nil {
		log.Printf("parsing aad response body: %v", err)
		return
	}

	e.Parsed = &er
}

// Marker method to indicate this as non-retriable when executed within an armruntime pipeline
func (e *AuthFailedError) NonRetriable() {
}

func (e *AuthFailedError) Unwrap() error {
	return e.innerErr
}

func (e *AuthFailedError) Error() string {
	if e.RawResp == nil { // non-http error, simply append inner error
		return fmt.Sprintf("%s: %s", authFailedPrefix, e.innerErr.Error())
	}

	if e.Parsed == nil { // unable to parse, provide HTTP error details
		return fmt.Sprintf("%s: %s", authFailedPrefix, e.httpErrorDetails())
	}

	// Provide user-friendly error message based on the parsed response.
	return fmt.Sprintf(
		"%s:\n(%s) %s\n",
		authFailedPrefix,
		e.Parsed.Error,
		// ErrorDescription contains multiline messaging that has TraceID, CorrelationID,
		// and other useful information embedded in it. Thus, it is not required to log other response body fields.
		e.Parsed.ErrorDescription)
}

func (e *AuthFailedError) httpErrorDetails() string {
	msg := &bytes.Buffer{}
	fmt.Fprintf(msg, //nolint:gosec // G705: writing to bytes.Buffer, not an HTTP response
		"%s %s://%s%s\n",
		e.RawResp.Request.Method,
		e.RawResp.Request.URL.Scheme,
		e.RawResp.Request.URL.Host,
		e.RawResp.Request.URL.Path)
	fmt.Fprintln(msg, "--------------------------------------------------------------------------------")
	fmt.Fprintf(msg, "RESPONSE %d: %s\n", e.RawResp.StatusCode, e.RawResp.Status)
	fmt.Fprintln(msg, "--------------------------------------------------------------------------------")
	body, err := io.ReadAll(e.RawResp.Body)
	e.RawResp.Body.Close()
	if err != nil {
		fmt.Fprintf(msg, "Error reading response body: %v", err)
	} else if len(body) > 0 {
		e.RawResp.Body = io.NopCloser(bytes.NewReader(body))
		if err := json.Indent(msg, body, "", "  "); err != nil {
			// failed to pretty-print so just dump it verbatim
			fmt.Fprint(msg, string(body))
		}
	} else {
		fmt.Fprintln(msg, "Response contained no body")
	}
	fmt.Fprintln(msg, "\n--------------------------------------------------------------------------------")
	return msg.String()
}
