// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"
)

// PortalPrefix holds the parsed project context needed to construct Foundry portal URLs.
type PortalPrefix struct {
	prefix string // e.g. "https://ai.azure.com/nextgen/r/<sub>,<rg>,,<account>,<project>"
}

// NewPortalPrefix parses an ARM project resource ID and returns a PortalPrefix
// that can be reused to build multiple portal URLs.
// Returns an error if the resource ID is invalid or not a Foundry project.
func NewPortalPrefix(projectResourceID string) (*PortalPrefix, error) {
	resourceID, err := arm.ParseResourceID(projectResourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse project resource ID: %w", err)
	}

	encodedSub, err := encodeSubscriptionForURL(resourceID.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to encode subscription ID: %w", err)
	}

	if resourceID.Parent == nil ||
		!strings.Contains(string(resourceID.ResourceType.Type), "/") {
		return nil, fmt.Errorf(
			"resource ID does not represent a Foundry project (missing parent account): %s",
			projectResourceID,
		)
	}

	prefix := fmt.Sprintf(
		"https://ai.azure.com/nextgen/r/%s,%s,,%s,%s",
		encodedSub, resourceID.ResourceGroupName,
		resourceID.Parent.Name, resourceID.Name,
	)
	return &PortalPrefix{prefix: prefix}, nil
}

// EvalRunURL returns the portal URL for an eval run report.
func (p *PortalPrefix) EvalRunURL(evalID, runID string) string {
	return fmt.Sprintf("%s/build/evaluations/%s/run/%s", p.prefix, evalID, runID)
}

// EvaluatorURL returns the portal URL for a generated evaluator.
func (p *PortalPrefix) EvaluatorURL(evaluatorName, version string) string {
	return fmt.Sprintf("%s/build/evaluations/catalog/%s/%s", p.prefix, evaluatorName, version)
}

// DatasetURL returns the portal URL for a dataset.
func (p *PortalPrefix) DatasetURL(datasetName, version string) string {
	return fmt.Sprintf("%s/build/data/datasets/%s/%s", p.prefix, datasetName, version)
}

// encodeSubscriptionForURL encodes a subscription ID GUID as base64 without padding.
func encodeSubscriptionForURL(subscriptionID string) (string, error) {
	guid, err := uuid.Parse(subscriptionID)
	if err != nil {
		return "", fmt.Errorf("invalid subscription ID format: %w", err)
	}
	guidBytes, _ := guid.MarshalBinary()
	return strings.TrimRight(base64.URLEncoding.EncodeToString(guidBytes), "="), nil
}
