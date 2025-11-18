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

	msal "github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
	"github.com/azure/azure-dev/internal"
	"github.com/azure/azure-dev/pkg/cloud"
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

	helpLink string
}

// newReLoginRequiredError returns an error if the response indicates that the user needs to reauthenticate.
// If it is not a reauthentication error, it returns false.
func newReLoginRequiredError(
	response *AadErrorResponse,
	scopes []string,
	cloud *cloud.Cloud,
) (error, bool) {
	if response == nil {
		return nil, false
	}

	//nolint:lll
	// https://learn.microsoft.com/azure/active-directory/develop/reference-aadsts-error-codes#handling-error-codes-in-your-application
	switch response.Error {
	case "invalid_grant",
		"interaction_required":
		err := ReLoginRequiredError{}
		err.init(response, scopes, cloud)
		suggestion := fmt.Sprintf("Suggestion: %s, run `%s` to acquire a new token.", err.scenario, err.loginCmd)
		if err.helpLink != "" {
			suggestion += fmt.Sprintf(" See %s for more info.", err.helpLink)
		}
		return &internal.ErrorWithSuggestion{
			Err:        &err,
			Suggestion: suggestion,
		}, true
	}

	return nil, false
}

func (e *ReLoginRequiredError) init(response *AadErrorResponse, scopes []string, cloud *cloud.Cloud) {
	e.errText = response.ErrorDescription
	e.scenario = "reauthentication required"
	e.loginCmd = "azd auth login"

	loginScopes := LoginScopesFull(cloud)
	for _, scope := range scopes {
		// filter out default login scopes
		if !slices.Contains(loginScopes, scope) {
			e.loginCmd += fmt.Sprintf(" --scope %s", scope)
		}
	}

	// The refresh token has expired or is invalid due to sign-in frequency checks by Conditional Access.
	if slices.Contains(response.ErrorCodes, 70043) {
		e.scenario = "login expired"
	}

	// In a Codespaces environment, `azd auth login` defaults to device code flow, which can cause issues
	// getting tokens if the Entra tenant has Conditional Access Policies set.
	if slices.Contains(response.ErrorCodes, 50005) {
		e.loginCmd += " --use-device-code=false"
		e.helpLink = "https://aka.ms/azd/troubleshoot/conditional-access-policy"
	}
}

func (e *ReLoginRequiredError) Error() string {
	return e.errText
}

// Marker method to indicate this as non-retriable when executed within an armruntime pipeline
func (e *ReLoginRequiredError) NonRetriable() {
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
	var msalCallErr msal.CallErr
	var authFailedErr *AuthFailedError
	var res *http.Response
	if errors.As(err, &msalCallErr) {
		res = msalCallErr.Resp
	} else if errors.As(err, &authFailedErr) { // in case this is re-thrown in a retry loop
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
	fmt.Fprintf(msg,
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
