package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	msal "github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
)

const authFailedPrefix string = "failed to authenticate"

// An error response from Azure Active Directory.
// See https://www.rfc-editor.org/rfc/rfc6749#section-5.2 for OAuth 2.0 spec
// See https://learn.microsoft.com/en-us/azure/active-directory/develop/reference-aadsts-error-codes#handling-error-codes-in-your-application for AAD error spec
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
type AuthFailedError struct {
	// The HTTP response motivating the error.
	rawResp *http.Response
	// Underlying error
	err error
	// The successfully parsed error response, if any
	parsed *AadErrorResponse

	loginCmd string
}

func newAuthFailedError(err error, loginCmd string) error {
	var msalCallErr msal.CallErr
	var res *http.Response
	if errors.As(err, &msalCallErr) {
		res = msalCallErr.Resp
	}

	if res == nil { // no response available, provide error wrapping
		return fmt.Errorf("%s: %w", authFailedPrefix, err)
	}

	e := &AuthFailedError{rawResp: res, loginCmd: loginCmd}
	e.parseResponse()
	return e
}

func (e *AuthFailedError) parseResponse() {
	body, err := io.ReadAll(e.rawResp.Body)
	e.rawResp.Body.Close()
	if err != nil {
		log.Printf("error reading aad response body: %v", err)
		return
	}
	e.rawResp.Body = io.NopCloser(bytes.NewReader(body))

	var er AadErrorResponse
	if err := json.Unmarshal(body, &er); err != nil {
		log.Printf("parsing aad response body: %v", err)
		return
	}

	e.parsed = &er
}

func (e *AuthFailedError) Unwrap() error {
	return e.err
}

func (e *AuthFailedError) Error() string {
	if e.parsed == nil { // unable to parse, provide HTTP error details
		return fmt.Sprintf("%s: %s", authFailedPrefix, e.httpErrorDetails())
	}

	switch e.parsed.Error {
	case "invalid_grant",
		"interaction_required":
		// log the error in case this needs further diagnosis
		log.Println(e.httpErrorDetails())
		return fmt.Sprintf("re-authentication required, run `%s` to login", e.loginCmd)
	}

	// ErrorDescription contains multiline messaging that has TraceID, CorrelationID,
	// and other useful information embedded in it.
	return fmt.Sprintf(
		"%s:\n(%s) %s\n",
		authFailedPrefix,
		e.parsed.Error,
		e.parsed.ErrorDescription)
}

func (e *AuthFailedError) httpErrorDetails() string {
	msg := &bytes.Buffer{}
	fmt.Fprintf(msg,
		"%s %s://%s%s\n",
		e.rawResp.Request.Method,
		e.rawResp.Request.URL.Scheme,
		e.rawResp.Request.URL.Host,
		e.rawResp.Request.URL.Path)
	fmt.Fprintln(msg, "--------------------------------------------------------------------------------")
	fmt.Fprintf(msg, "RESPONSE %d: %s\n", e.rawResp.StatusCode, e.rawResp.Status)
	fmt.Fprintln(msg, "--------------------------------------------------------------------------------")
	body, err := io.ReadAll(e.rawResp.Body)
	e.rawResp.Body.Close()
	if err != nil {
		fmt.Fprintf(msg, "Error reading response body: %v", err)
	} else if len(body) > 0 {
		e.rawResp.Body = io.NopCloser(bytes.NewReader(body))
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
