// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"fmt"

	"net/url"
	"strings"
)

func serviceLink(endpoint, apiVersion, value, currentPath string) (string, url.Values, error) {
	base, err := url.Parse(endpoint + currentPath)
	if err != nil {
		return "", nil, malformedResponse()
	}
	link, err := url.Parse(value)
	if err != nil || value == "" || link.User != nil || strings.Contains(value, "#") ||
		link.Opaque != "" || link.RawPath != "" || strings.Contains(link.Path, "\\") {
		return "", nil, malformedResponse()
	}
	for part := range strings.SplitSeq(link.Path, "/") {
		if part == "." || part == ".." {
			return "", nil, malformedResponse()
		}
	}
	target := base.ResolveReference(link)
	prefix := "/contentunderstanding"
	if target.Scheme != "https" || !strings.EqualFold(target.Host, base.Host) ||
		!strings.HasPrefix(target.Path, prefix+"/") {
		return "", nil, malformedResponse()
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return "", nil, malformedResponse()
	}
	if versions, present := query["api-version"]; present && (len(versions) != 1 || versions[0] != apiVersion) {
		return "", nil, malformedResponse()
	}
	query.Set("api-version", apiVersion)
	return strings.TrimPrefix(target.Path, prefix), query, nil
}

func operationError(operation, status string, problem *wireDiagnostic) *Error {
	result := &Error{
		Kind: ErrorService, Code: operation + "_operation_failed",
		Message:    fmt.Sprintf("The %s operation failed.", operation),
		Suggestion: "Check the analyzer configuration, input, and dependencies before retrying.",
	}
	if strings.EqualFold(status, "canceled") || strings.EqualFold(status, "cancelled") {
		result.Kind = ErrorCancelled
		result.Code = operation + "_operation_canceled"
		result.Message = fmt.Sprintf("The %s operation was cancelled.", operation)
	}
	if problem != nil {
		if serviceCodePattern.MatchString(problem.Code) {
			result.Code = problem.Code
		}
		result.Details = diagnostics(problem.Details, true)
	}
	return result
}
