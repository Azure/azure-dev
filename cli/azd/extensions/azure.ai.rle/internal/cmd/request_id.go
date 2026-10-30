// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var correlationIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

func serviceRequestID(headers http.Header, body []byte) string {
	for _, header := range []string{"x-ms-request-id", "apim-request-id", "x-request-id", "request-id"} {
		if value := strings.TrimSpace(headers.Get(header)); correlationIDPattern.MatchString(value) {
			return value
		}
	}
	var details struct {
		Correlation struct {
			Request string `json:"request"`
		} `json:"correlation"`
	}
	if json.Unmarshal(body, &details) == nil && correlationIDPattern.MatchString(details.Correlation.Request) {
		return details.Correlation.Request
	}
	return ""
}

func withRequestID(message string, requestID string) string {
	if requestID == "" {
		return message
	}
	return fmt.Sprintf("%s\nRequest ID: %s", message, requestID)
}

func withRequestIDError(err error, requestID string) error {
	if requestID == "" {
		return err
	}
	return fmt.Errorf("%w\nRequest ID: %s", err, requestID)
}
