// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

// This file mirrors the wire-level error vocabulary emitted by the Foundry
// hosted-agents service so the extension can react without re-classifying.
// String values must stay byte-for-byte identical to the platform's
// `UserErrorCode`, `SessionErrorCode`, and `AgentVersionStatus` enums.
//
// Authoritative sources (vienna repo):
//   - Services/HostedAgents/Common/Exceptions/UserErrorCode.cs
//   - Services/HostedAgents/Session/Exceptions/SessionErrorCode.cs
//   - Contracts/V2/Generated/Agents/AgentVersionStatus.cs
//
// The platform already appends `aka.ms/hostedagents/tsg/{image,code,
// provisioning,readme}` to user-facing deploy-failure messages via
// `WithTroubleshootingInfo`; the extension surfaces those messages
// verbatim and never re-derives a TSG link.

// UserErrorCode is the platform's deploy-time error classification.
// Emitted on agent-version creation failures.
type UserErrorCode string

const (
	// UserErrorImage covers ACR auth failures, unknown manifests, wrong
	// architecture, DNS issues, and 403s on image pull.
	UserErrorImage UserErrorCode = "ImageError"
	// UserErrorCodeBlob covers code-blob 404s, ACL problems, and dependency
	// resolution failures.
	UserErrorCodeBlob UserErrorCode = "CodeError"
	// UserErrorProvisioning is the catch-all for unclassified platform
	// failures during agent version creation.
	UserErrorProvisioning UserErrorCode = "ProvisioningError"
)

// SessionErrorCode is the platform's invoke-time error classification.
// Surfaced on the `x-adc-response-details` response header (and inside
// the response body for some codes).
type SessionErrorCode string

const (
	// SessionReadinessTimeout (HTTP 502 upstream): container was slow to
	// bind its port.
	SessionReadinessTimeout SessionErrorCode = "ReadinessTimeout"
	// SessionProxyTimeout (HTTP 504 upstream): container hung mid-request.
	SessionProxyTimeout SessionErrorCode = "ProxyTimeout"
	// SessionSandboxIdle (HTTP 502 upstream + "not available" body): the
	// session was paused for idleness and auto-resumes on retry.
	SessionSandboxIdle SessionErrorCode = "SandboxIdle"
	// SessionSandboxNotFound (HTTP 404 platform): the session was purged.
	SessionSandboxNotFound SessionErrorCode = "SandboxNotFound"
	// SessionQuotaExceeded (HTTP 429): per-subscription session quota hit.
	SessionQuotaExceeded SessionErrorCode = "QuotaExceeded"
	// SessionRegionalQuotaExceeded (HTTP 429): regional capacity full.
	SessionRegionalQuotaExceeded SessionErrorCode = "RegionalQuotaExceeded"
	// SessionAgentVersionNotReady: deploy is still in progress.
	SessionAgentVersionNotReady SessionErrorCode = "AgentVersionNotReady"
	// SessionAgentVersionProvisioningFailed: deploy failed; `show` surfaces
	// the structured error.
	SessionAgentVersionProvisioningFailed SessionErrorCode = "AgentVersionProvisioningFailed"
)

// AgentVersionStatus mirrors the platform's lifecycle states for a
// deployed agent version.
type AgentVersionStatus string

const (
	// AgentVersionCreating indicates the deploy is still in progress.
	AgentVersionCreating AgentVersionStatus = "Creating"
	// AgentVersionActive indicates the deploy succeeded and the agent is
	// ready to receive invocations.
	AgentVersionActive AgentVersionStatus = "Active"
	// AgentVersionFailed indicates the deploy failed; the error payload
	// carries the structured reason.
	AgentVersionFailed AgentVersionStatus = "Failed"
	// AgentVersionDeleting indicates a delete is in flight.
	AgentVersionDeleting AgentVersionStatus = "Deleting"
	// AgentVersionDeleted indicates the version has been removed; a
	// follow-up `azd deploy` is needed to redeploy.
	AgentVersionDeleted AgentVersionStatus = "Deleted"
)

// RemediationForUserErrorCode returns the suggestion to surface alongside
// a deploy failure with the given UserErrorCode. The platform's message
// already includes the TSG URL, so callers should print the verbatim
// message above the returned suggestion line.
//
// Returns ok=false for unrecognized codes; callers should fall back to a
// generic "see `azd ai agent show` for the failure reason" line.
func RemediationForUserErrorCode(code UserErrorCode) (primary Suggestion, ok bool) {
	switch code {
	case UserErrorImage:
		return Suggestion{
			Command:     "azd ai agent monitor --type system --follow",
			Description: "watch deploy logs for the image-pull failure",
		}, true
	case UserErrorCodeBlob:
		return Suggestion{
			Command:     "azd ai agent monitor --type system --follow",
			Description: "watch deploy logs for the code-package failure",
		}, true
	case UserErrorProvisioning:
		return Suggestion{
			Command:     "azd ai agent show",
			Description: "view the structured deploy error and follow the linked TSG",
		}, true
	}
	return Suggestion{}, false
}

// RemediationForSessionErrorCode returns the suggestion(s) to surface
// alongside an invoke failure with the given SessionErrorCode. Some codes
// produce a secondary action (e.g., quota-exceeded points to the
// session-list command); others return primary only with secondary nil.
//
// Returns ok=false for unrecognized codes; callers should fall back to
// "Run `azd ai agent monitor --tail 100` for container logs."
func RemediationForSessionErrorCode(code SessionErrorCode) (primary Suggestion, secondary *Suggestion, ok bool) {
	switch code {
	case SessionReadinessTimeout:
		return Suggestion{
				Command:     "azd ai agent invoke",
				Description: "retry — the container was slow to bind its port",
			},
			&Suggestion{
				Command:     "azd ai agent monitor --type system",
				Description: "check startup logs if retries continue to fail",
			}, true
	case SessionProxyTimeout:
		return Suggestion{
				Command:     "azd ai agent monitor --tail 100",
				Description: "the container hung mid-request — inspect recent logs",
			},
			nil, true
	case SessionSandboxIdle:
		return Suggestion{
				Command:     "azd ai agent invoke",
				Description: "retry — the session was paused and auto-resumes",
			},
			nil, true
	case SessionSandboxNotFound:
		return Suggestion{
				Command:     "azd ai agent invoke",
				Description: "the previous session expired — retry to start a fresh one",
			},
			nil, true
	case SessionQuotaExceeded:
		return Suggestion{
				Command:     "azd ai agent session list",
				Description: "session quota reached — delete unused sessions",
			},
			nil, true
	case SessionRegionalQuotaExceeded:
		return Suggestion{
				Command:     "azd provision",
				Description: "regional capacity full — re-provision in a different region",
			},
			nil, true
	case SessionAgentVersionNotReady:
		return Suggestion{
				Command:     "azd ai agent show",
				Description: "deploy still in progress — poll until status is Active",
			},
			nil, true
	case SessionAgentVersionProvisioningFailed:
		return Suggestion{
				Command:     "azd ai agent show",
				Description: "deploy failed — view the structured error and linked TSG",
			},
			nil, true
	}
	return Suggestion{}, nil, false
}
