// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore nextgen unpadded

package foundrypolicy

import "github.com/azure/azure-dev/cli/shared/foundry/policy"

// ParsedProjectID holds the segments extracted from a Foundry project ARM resource ID.
type ParsedProjectID = policy.ParsedProjectID

// ParseFoundryProjectID parses a Foundry project ARM resource ID into its component segments.
func ParseFoundryProjectID(projectID string) (*ParsedProjectID, bool) {
	return policy.ParseFoundryProjectID(projectID)
}

// IsValidPolicyAssignmentID reports whether assignmentID is a well-formed ARM resource ID
// for a Microsoft.Authorization/policyAssignments resource.
func IsValidPolicyAssignmentID(assignmentID string) bool {
	return policy.IsValidPolicyAssignmentID(assignmentID)
}

// EncodeSubscriptionIDForPortal encodes a subscription GUID as 16 binary bytes in
// unpadded base64url, falling back to unpadded base64url of the raw string when
// the subscription ID is not a 32-hex-digit GUID.
func EncodeSubscriptionIDForPortal(subscriptionID string) string {
	return policy.EncodeSubscriptionIDForPortal(subscriptionID)
}

// BuildFoundryProjectRouteSegment builds the `/nextgen/r/{base64Sub},{rg},,{account},{project}`
// route segment shared by Foundry Portal deep links, with commas percent-encoded as `%2C`.
func BuildFoundryProjectRouteSegment(parsed ParsedProjectID) string {
	return policy.BuildFoundryProjectRouteSegment(parsed)
}

// BuildPolicyAssignmentPortalURL constructs the Foundry Portal Manage -> Govern deep link
// for a blocking Azure Policy assignment.
func BuildPolicyAssignmentPortalURL(projectResourceID, assignmentID string) string {
	return policy.BuildPolicyAssignmentPortalURL(projectResourceID, assignmentID)
}
