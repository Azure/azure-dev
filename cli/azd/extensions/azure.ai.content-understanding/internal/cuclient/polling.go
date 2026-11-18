// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
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
		Message: fmt.Sprintf("The %s operation failed.", operation),
		Suggestion: "Correct the reported service problem before retrying. " +
			"Use azd ai cu analyzer show <ANALYZER_NAME> to inspect the analyzer configuration. " +
			"If the cause is unclear, report the error codes to support.",
	}
	if strings.EqualFold(status, "canceled") || strings.EqualFold(status, "cancelled") {
		result.Kind = ErrorCancelled
		result.Code = operation + "_operation_canceled"
		result.Message = fmt.Sprintf("The %s operation was cancelled.", operation)
	}
	if problem != nil {
		if serviceCodePattern.MatchString(problem.Code) {
			result.Code = problem.Code
			result.Message += fmt.Sprintf(" Service code: %s.", problem.Code)
		}
	}
	appendServiceDiagnostics(result, problem)
	return result
}

func appendServiceDiagnostics(result *Error, problem *wireDiagnostic) {
	var values []Diagnostic
	remaining := 32
	omitted := false
	var visit func(Diagnostic, int)
	visit = func(value Diagnostic, depth int) {
		if remaining == 0 || depth == 8 {
			omitted = true
			return
		}
		remaining--
		for _, child := range value.Details {
			visit(child, depth+1)
		}
		values = append(values, value)
	}
	if problem != nil {
		result.Details = diagnostics(problem.Details, true)
		visit(Diagnostic{
			Code: problem.Code, Message: RedactDiagnostic(problem.Message), Details: result.Details,
		}, 0)
	}
	modelNotSupported := result.Code == "ModelNotSupported"
	modelMessage := false
	shown := false
	// Prefer the deepest causes so a long outer error cannot hide the actionable reason.
	for _, value := range values {
		if value.Code == "ModelNotSupported" {
			modelNotSupported = true
		}
		if value.Message == "" && (value.Code == result.Code || !serviceCodePattern.MatchString(value.Code)) {
			continue
		}
		code := value.Code
		if !serviceCodePattern.MatchString(code) {
			code = "service_error"
		}
		line := "\n" + code
		if value.Message != "" {
			message := value.Message
			if len(message) > 500 {
				end := 470
				for !utf8.RuneStart(message[end]) {
					end--
				}
				message = message[:end] + "... [diagnostic truncated]"
			}
			line += ": " + message
		}
		if len(result.Message)+len(line) > 900 {
			omitted = true
			continue
		}
		result.Message += line
		shown = true
		if value.Code == "ModelNotSupported" && value.Message != "" {
			modelMessage = true
		}
	}
	if omitted {
		result.Message += "\nAdditional service diagnostics omitted."
	}
	if !shown && !omitted {
		result.Message += " No further service diagnostic was provided."
	}
	if modelNotSupported && result.Kind != ErrorCancelled {
		if !modelMessage {
			result.Message += " ModelNotSupported: The configured model is not supported by this analyzer."
		}
		result.Suggestion = "Inspect supportedModels with azd ai cu analyzer show <ANALYZER_NAME> " +
			"and resource modelDeployments with azd ai cu defaults show. " +
			"With resource-owner approval, map the analyzer model alias to a supported deployed model; " +
			"defaults changes are resource-wide."
	}
}
