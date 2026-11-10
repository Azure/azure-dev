// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore nextgen unpadded

package foundrypolicy

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const defaultFoundryGovernPortalURL = "https://ai.azure.com/?flight=govern_ignite"

var foundryProjectIDPattern = regexp.MustCompile(
	`(?i)/subscriptions/([^/?#]+)/resourceGroups/([^/?#]+)/providers/[^/?#]+/accounts/([^/?#]+)/projects/([^/?#]+)$`,
)

var hexGUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{32}$`)

// ParsedProjectID holds the segments extracted from a Foundry project ARM resource ID.
type ParsedProjectID struct {
	SubscriptionID      string
	ResourceGroup       string
	ProjectResourceName string
	ProjectName         string
}

// ParseFoundryProjectID parses a Foundry project ARM resource ID into its component segments.
// Expected shape:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/accounts/{account}/projects/{project}
func ParseFoundryProjectID(projectID string) (*ParsedProjectID, bool) {
	trimmed := strings.TrimSpace(projectID)
	if trimmed == "" {
		return nil, false
	}
	matches := foundryProjectIDPattern.FindStringSubmatch(trimmed)
	if len(matches) != 5 {
		return nil, false
	}
	return &ParsedProjectID{
		SubscriptionID:      matches[1],
		ResourceGroup:       matches[2],
		ProjectResourceName: matches[3],
		ProjectName:         matches[4],
	}, true
}

// EncodeSubscriptionIDForPortal encodes a subscription GUID as 16 binary bytes in
// unpadded base64url, falling back to unpadded base64url of the raw string when
// the subscription ID is not a 32-hex-digit GUID.
func EncodeSubscriptionIDForPortal(subscriptionID string) string {
	cleanHex := strings.ReplaceAll(subscriptionID, "-", "")
	if hexGUIDPattern.MatchString(cleanHex) {
		if rawBytes, err := hex.DecodeString(cleanHex); err == nil {
			return base64.RawURLEncoding.EncodeToString(rawBytes)
		}
	}
	return base64.RawURLEncoding.EncodeToString([]byte(subscriptionID))
}

// BuildFoundryProjectRouteSegment builds the `/nextgen/r/{base64Sub},{rg},,{account},{project}`
// route segment shared by Foundry Portal deep links, with commas percent-encoded as `%2C`.
func BuildFoundryProjectRouteSegment(parsed ParsedProjectID) string {
	base64Sub := url.PathEscape(EncodeSubscriptionIDForPortal(parsed.SubscriptionID))
	return fmt.Sprintf(
		"%s%%2C%s%%2C%%2C%s%%2C%s",
		base64Sub,
		url.PathEscape(parsed.ResourceGroup),
		url.PathEscape(parsed.ProjectResourceName),
		url.PathEscape(parsed.ProjectName),
	)
}

// BuildPolicyAssignmentPortalURL constructs the Foundry Portal Manage -> Govern deep link
// for a blocking Azure Policy assignment. Falls back to the generic Foundry Govern URL
// when projectResourceID cannot be parsed.
func BuildPolicyAssignmentPortalURL(projectResourceID, assignmentID string) string {
	parsed, ok := ParseFoundryProjectID(projectResourceID)
	if !ok || strings.TrimSpace(assignmentID) == "" {
		return defaultFoundryGovernPortalURL
	}

	projectRoute := BuildFoundryProjectRouteSegment(*parsed)
	assignmentKey := base64.RawURLEncoding.EncodeToString([]byte(assignmentID))

	query := url.Values{}
	query.Set("subscriptionId", parsed.SubscriptionID)
	query.Set(
		"resourceGroupId",
		fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", parsed.SubscriptionID, parsed.ResourceGroup),
	)
	query.Set("projectId", projectResourceID)
	query.Set("flight", "govern_ignite")

	// Construct URL preserving %2C in the route segment exactly as Foundry Portal expects.
	rawPath := fmt.Sprintf("/nextgen/r/%s/manage/govern/assignments/%s", projectRoute, assignmentKey)
	encodedQuery := strings.ReplaceAll(query.Encode(), "+", "%20")
	return fmt.Sprintf("https://ai.azure.com%s?%s", rawPath, encodedQuery)
}
