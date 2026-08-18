// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"azureaieval/internal/messages"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"
)

// foundryProjectResourceType is the only resource a portal prefix can be built
// from. Every nested resource has a parent and a slash in its type, so matching
// on shape rather than on this let unrelated children through.
const foundryProjectResourceType = "Microsoft.CognitiveServices/accounts/projects"

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
		return nil, messages.ParsingProjectResourceID(err)
	}

	encodedSub, err := encodeSubscriptionForURL(resourceID.SubscriptionID)
	if err != nil {
		return nil, messages.EncodingSubscriptionID(err)
	}

	// The exact type, not merely a nested one. Any child resource has a parent
	// and a slash in its type -- a storage container reached this far and built a
	// plausible URL onto a portal page that does not exist.
	if resourceID.Parent == nil ||
		!strings.EqualFold(resourceID.ResourceType.String(), foundryProjectResourceType) {
		return nil, messages.NotAFoundryProjectResourceID(projectResourceID)
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
	return fmt.Sprintf("%s/build/evaluations/%s/run/%s",
		p.prefix, url.PathEscape(evalID), url.PathEscape(runID))
}

// EvaluatorURL returns the portal URL for a generated evaluator.
func (p *PortalPrefix) EvaluatorURL(evaluatorName, version string) string {
	return fmt.Sprintf("%s/build/evaluations/catalog/%s/%s",
		p.prefix, url.PathEscape(evaluatorName), url.PathEscape(version))
}

// DatasetURL returns the portal URL for a dataset.
//
// Escaped rather than interpolated: these names are the service's, not this
// extension's, so a space or a slash in one would otherwise produce a link that
// breaks when pasted or points somewhere else entirely.
func (p *PortalPrefix) DatasetURL(datasetName, version string) string {
	return fmt.Sprintf("%s/build/data/datasets/%s/%s",
		p.prefix, url.PathEscape(datasetName), url.PathEscape(version))
}

// OptimizationURL returns the portal URL for an optimization job.
func (p *PortalPrefix) OptimizationURL(agentName, operationID string) string {
	return fmt.Sprintf("%s/build/agents/%s/optimization/%s",
		p.prefix, url.PathEscape(agentName), url.PathEscape(operationID))
}

// encodeSubscriptionForURL encodes a subscription ID GUID as base64 without padding.
func encodeSubscriptionForURL(subscriptionID string) (string, error) {
	guid, err := uuid.Parse(subscriptionID)
	if err != nil {
		return "", messages.InvalidSubscriptionID(err)
	}
	guidBytes, _ := guid.MarshalBinary()
	return strings.TrimRight(base64.URLEncoding.EncodeToString(guidBytes), "="), nil
}
