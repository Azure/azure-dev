// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package analyzercopy implements non-replacing analyzer copy orchestration.
package analyzercopy

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"azure.ai.content-understanding/internal/cuclient"
)

type Request struct {
	SourceID, DestinationID                  string
	SourceResourceID, SourceRegion           string
	DestinationResourceID, DestinationRegion string
}

func Copy(
	ctx context.Context, source, destination cuclient.Client, request Request,
) (cuclient.AnalyzerResponse, error) {
	empty := cuclient.AnalyzerResponse{}
	cross := request.SourceResourceID != "" || request.DestinationResourceID != "" ||
		request.SourceRegion != "" || request.DestinationRegion != ""
	if cross && (request.SourceResourceID == "" || request.SourceRegion == "" ||
		request.DestinationResourceID == "" || request.DestinationRegion == "") {
		return empty, &cuclient.Error{
			Kind: cuclient.ErrorValidation, Code: "incomplete_copy_coordinates",
			Message: "Cross-resource copy requires both account IDs and regions.",
		}
	}
	copyRequest := cuclient.CopyAnalyzerRequest{
		SourceAnalyzerID: request.SourceID, DestinationAnalyzerID: request.DestinationID,
		SourceAzureResourceID: request.SourceResourceID, SourceRegion: request.SourceRegion,
	}
	if err := copyRequest.Validate(); err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	analyzer, err := source.GetAnalyzer(ctx, request.SourceID)
	if err != nil {
		if missing(err) {
			return empty, &cuclient.Error{
				Kind: cuclient.ErrorNotFound, Code: "copy_source_not_found", StatusCode: 404,
				Message:    "The source analyzer was not found.",
				Suggestion: "Check the source analyzer ID and source resource.", Cause: err,
			}
		}
		return empty, scopedError(err, "source", false)
	}
	if cross {
		dependencies, err := customDependencies(analyzer.JSON)
		if err != nil {
			return empty, err
		}
		var absent []string
		for _, id := range dependencies {
			if err := ctx.Err(); err != nil {
				return empty, err
			}
			if _, err := destination.GetAnalyzer(ctx, id); err != nil {
				if !missing(err) {
					return empty, scopedError(err, "destination", false)
				}
				absent = append(absent, id)
			}
		}
		if len(absent) != 0 {
			return empty, &cuclient.Error{
				Kind: cuclient.ErrorValidation, Code: "missing_copy_dependencies",
				Message: "Custom content-category analyzers are missing at the destination: " +
					strings.Join(absent, ", ") + ".",
				Suggestion: "Copy these dependencies explicitly first; dependencies are not copied automatically.",
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if _, err := destination.GetAnalyzer(ctx, request.DestinationID); err == nil {
		return empty, destinationExists(nil)
	} else if !missing(err) {
		return empty, scopedError(err, "destination", false)
	}
	if cross {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		_, err := source.GrantCopyAuthorization(ctx, cuclient.GrantCopyAuthorizationRequest{
			AnalyzerID:            request.SourceID,
			TargetAzureResourceID: request.DestinationResourceID, TargetRegion: request.DestinationRegion,
		})
		if err != nil {
			return empty, scopedError(err, "source", false)
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	result, err := destination.CopyAnalyzer(ctx, copyRequest)
	if err != nil {
		if domain, ok := errors.AsType[*cuclient.Error](err); ok {
			if domain.Kind == cuclient.ErrorConflict {
				return empty, destinationExists(err)
			}
			if domain.Code == "analyzer_operation_failed" {
				return empty, failedCopy(err)
			}
		}
		return empty, scopedError(err, "destination", cross)
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if strings.EqualFold(result.Analyzer.Status, "failed") {
		return empty, failedCopy(nil)
	}
	return result, nil
}

func failedCopy(cause error) error {
	return &cuclient.Error{
		Kind: cuclient.ErrorService, Code: "copy_failed",
		Message:    "The copied analyzer has failed status at the destination.",
		Suggestion: "Inspect the analyzer at the destination resource before explicit deletion or retrying with a new ID.",
		Cause:      cause,
	}
}

func missing(err error) bool {
	domain, ok := errors.AsType[*cuclient.Error](err)
	return ok && domain.Kind == cuclient.ErrorNotFound && (domain.StatusCode == 0 || domain.StatusCode == 404)
}

func destinationExists(cause error) error {
	return &cuclient.Error{
		Kind: cuclient.ErrorConflict, Code: "copy_destination_exists", StatusCode: 409,
		Message:    "The destination analyzer already exists; nothing was replaced.",
		Suggestion: "Use a new versioned destination ID, or inspect and explicitly delete it at the destination resource.",
		Cause:      cause,
	}
}

func scopedError(err error, side string, grant bool) error {
	domain, ok := errors.AsType[*cuclient.Error](err)
	if !ok || domain.Kind != cuclient.ErrorAuthentication {
		return err
	}
	suggestion := "Run azd auth login with the intended identity and check Cognitive Services User access on the " +
		side + " resource."
	if grant {
		suggestion += " The copy grant may have expired; retry the copy command to request a fresh authorization."
	}
	return &cuclient.Error{
		Kind: cuclient.ErrorAuthentication, Code: "copy_" + side + "_authorization_failed",
		StatusCode: domain.StatusCode,
		Message:    "The identity could not authorize the analyzer copy on the " + side + " resource.",
		Suggestion: suggestion, Cause: err,
	}
}

func customDependencies(raw json.RawMessage) ([]string, error) {
	var analyzer map[string]json.RawMessage
	if json.Unmarshal(raw, &analyzer) != nil || analyzer == nil {
		return nil, invalidDefinition()
	}
	config := analyzer["config"]
	if len(config) == 0 || string(config) == "null" {
		return nil, nil
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal(config, &settings) != nil || settings == nil {
		return nil, invalidDefinition()
	}
	categories := settings["contentCategories"]
	if len(categories) == 0 {
		categories = settings["content_categories"]
	}
	if len(categories) == 0 || string(categories) == "null" {
		return nil, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(categories, &entries) != nil {
		var mapping map[string]json.RawMessage
		if json.Unmarshal(categories, &mapping) != nil || mapping == nil {
			return nil, invalidDefinition()
		}
		for _, entry := range mapping {
			entries = append(entries, entry)
		}
	}
	var dependencies []string
	for _, raw := range entries {
		var category map[string]json.RawMessage
		if json.Unmarshal(raw, &category) != nil || category == nil {
			return nil, invalidDefinition()
		}
		value := category["analyzerId"]
		if len(value) == 0 {
			value = category["analyzer_id"]
		}
		if len(value) == 0 || string(value) == "null" {
			continue
		}
		var id string
		if json.Unmarshal(value, &id) != nil || cuclient.ValidateAnalyzerID(id) != nil {
			return nil, invalidDefinition()
		}
		if !strings.HasPrefix(id, "prebuilt-") {
			dependencies = append(dependencies, id)
		}
	}
	slices.Sort(dependencies)
	return slices.Compact(dependencies), nil
}

func invalidDefinition() error {
	return &cuclient.Error{
		Kind: cuclient.ErrorService, Code: "invalid_copy_source",
		Message: "The source analyzer returned invalid content-category configuration.",
	}
}
