// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore nextgen unpadded managementgroups

package policy

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"
)

const defaultFoundryGovernPortalURL = "https://ai.azure.com/?flight=govern_ignite"

var (
	foundryProjectIDPattern = regexp.MustCompile(
		`(?i)^/subscriptions/([^/?#\\\s@]+)/resourceGroups/([^/?#\\\s@]+)/` +
			`providers/Microsoft\.CognitiveServices/accounts/([^/?#\\\s@]+)/projects/([^/?#\\\s@]+)$`,
	)
	hexGUIDPattern                = regexp.MustCompile(`(?i)^[0-9a-f]{32}$`)
	policyAssignmentSuffixPattern = regexp.MustCompile(
		`(?i)/providers/Microsoft\.Authorization/policyAssignments/[^/]+$`,
	)
	managementGroupScopePattern = regexp.MustCompile(
		`(?i)^/providers/Microsoft\.Management/managementGroups/([^/]+)$`,
	)
	subscriptionScopeHierarchyPattern = regexp.MustCompile(
		`(?i)^/subscriptions/([^/]+)(?:/resourceGroups/[^/]+(?:/providers/[^/]+(?:/[^/]+/[^/]+)+)?)?$`,
	)
)

// ParsedProjectID holds the segments extracted from a Foundry project ARM resource ID.
type ParsedProjectID struct {
	SubscriptionID      string
	ResourceGroup       string
	ProjectResourceName string
	ProjectName         string
}

func hasDotOrEmptySegments(resourceID string) bool {
	if strings.Contains(resourceID, "//") {
		return true
	}
	trimmed := strings.Trim(resourceID, "/")
	for seg := range strings.SplitSeq(trimmed, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// ParseFoundryProjectID parses a Foundry project ARM resource ID into its component segments.
// Expected shape:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/projects/{project}
func ParseFoundryProjectID(projectID string) (*ParsedProjectID, bool) {
	raw := strings.TrimSpace(projectID)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.Contains(raw, "//") {
		return nil, false
	}
	trimmed := strings.TrimSuffix(raw, "/")
	if trimmed == "" || hasDotOrEmptySegments(trimmed) {
		return nil, false
	}
	matches := foundryProjectIDPattern.FindStringSubmatch(trimmed)
	if len(matches) != 5 {
		return nil, false
	}
	if uuid.Validate(matches[1]) != nil {
		return nil, false
	}
	return &ParsedProjectID{
		SubscriptionID:      matches[1],
		ResourceGroup:       matches[2],
		ProjectResourceName: matches[3],
		ProjectName:         matches[4],
	}, true
}

// IsValidPolicyAssignmentID reports whether assignmentID is a well-formed ARM resource ID
// for a Microsoft.Authorization/policyAssignments resource at management-group, subscription,
// resource-group, or resource scope.
func IsValidPolicyAssignmentID(assignmentID string) bool {
	raw := strings.TrimSpace(assignmentID)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.Contains(raw, "//") {
		return false
	}
	trimmed := strings.TrimSuffix(raw, "/")
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") ||
		hasDotOrEmptySegments(trimmed) ||
		strings.ContainsAny(trimmed, "?#@\\ \t\r\n") {
		return false
	}
	resID, err := arm.ParseResourceID(trimmed)
	if err != nil || resID == nil ||
		!strings.EqualFold(resID.ResourceType.String(), "Microsoft.Authorization/policyAssignments") ||
		strings.TrimSpace(resID.Name) == "" ||
		resID.Name == "." || resID.Name == ".." {
		return false
	}
	loc := policyAssignmentSuffixPattern.FindStringIndex(trimmed)
	if loc == nil || loc[0] == 0 {
		return false
	}
	scope := trimmed[:loc[0]]
	if mgMatch := managementGroupScopePattern.FindStringSubmatch(scope); len(mgMatch) == 2 {
		return mgMatch[1] != "." && mgMatch[1] != ".."
	}
	subMatch := subscriptionScopeHierarchyPattern.FindStringSubmatch(scope)
	if len(subMatch) != 2 || uuid.Validate(subMatch[1]) != nil {
		return false
	}
	if strings.EqualFold(scope, "/subscriptions/"+subMatch[1]) {
		return true
	}
	scopeRes, err := arm.ParseResourceID(scope)
	return err == nil && scopeRes != nil && scopeRes.ResourceGroupName != ""
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
// when projectResourceID or assignmentID cannot be validated.
func BuildPolicyAssignmentPortalURL(projectResourceID, assignmentID string) string {
	parsed, ok := ParseFoundryProjectID(projectResourceID)
	if !ok || !IsValidPolicyAssignmentID(assignmentID) {
		return defaultFoundryGovernPortalURL
	}
	trimmedAssignment := strings.TrimSuffix(strings.TrimSpace(assignmentID), "/")
	trimmedProjectID := strings.TrimSuffix(strings.TrimSpace(projectResourceID), "/")

	projectRoute := BuildFoundryProjectRouteSegment(*parsed)
	assignmentKey := base64.RawURLEncoding.EncodeToString([]byte(trimmedAssignment))

	query := url.Values{}
	query.Set("subscriptionId", parsed.SubscriptionID)
	query.Set(
		"resourceGroupId",
		fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", parsed.SubscriptionID, parsed.ResourceGroup),
	)
	query.Set("projectId", trimmedProjectID)
	query.Set("flight", "govern_ignite")

	// Construct URL preserving %2C in the route segment exactly as Foundry Portal expects.
	rawPath := fmt.Sprintf("/nextgen/r/%s/manage/govern/assignments/%s", projectRoute, assignmentKey)
	encodedQuery := strings.ReplaceAll(query.Encode(), "+", "%20")
	return fmt.Sprintf("https://ai.azure.com%s?%s", rawPath, encodedQuery)
}
