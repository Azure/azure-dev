// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"azureaidataset/internal/messages"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"
)

// foundryProjectResourceType is the only resource a portal prefix can be built
// from. Every nested resource has a parent and a slash in its type, so matching
// on shape rather than on this lets unrelated children through.
const foundryProjectResourceType = "Microsoft.CognitiveServices/accounts/projects"

// PortalPrefix holds the parsed project context a Foundry portal URL is built
// from.
type PortalPrefix struct {
	prefix string // e.g. "https://ai.azure.com/nextgen/r/<sub>,<rg>,,<account>,<project>"
}

// NewPortalPrefix parses an ARM project resource ID, or refuses anything that
// is not a Foundry project.
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
	// and a slash in its type, so a storage container would otherwise build a
	// plausible URL onto a portal page that does not exist.
	if resourceID.Parent == nil ||
		!strings.EqualFold(resourceID.ResourceType.String(), foundryProjectResourceType) {
		return nil, messages.NotAFoundryProjectResourceID(projectResourceID)
	}

	return &PortalPrefix{prefix: fmt.Sprintf(
		"https://ai.azure.com/nextgen/r/%s,%s,,%s,%s",
		encodedSub, resourceID.ResourceGroupName,
		resourceID.Parent.Name, resourceID.Name,
	)}, nil
}

// DatasetURL returns the portal page for one dataset version.
func (p *PortalPrefix) DatasetURL(datasetName, version string) string {
	return fmt.Sprintf("%s/build/data/datasets/%s/%s",
		p.prefix, url.PathEscape(datasetName), url.PathEscape(version))
}

// encodeSubscriptionForURL encodes a subscription ID GUID as base64 without
// padding, which is the shape the portal route expects.
func encodeSubscriptionForURL(subscriptionID string) (string, error) {
	guid, err := uuid.Parse(subscriptionID)
	if err != nil {
		return "", messages.InvalidSubscriptionID(err)
	}
	guidBytes, _ := guid.MarshalBinary()
	return strings.TrimRight(base64.URLEncoding.EncodeToString(guidBytes), "="), nil
}
