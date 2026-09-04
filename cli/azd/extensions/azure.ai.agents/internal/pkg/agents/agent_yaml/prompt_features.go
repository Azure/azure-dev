// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"strings"

	"azureaiagent/internal/pkg/azure"
)

// ValidateRaiPolicyName rejects a policy value that is not a full ARM resource
// ID.
//
// The service reports a bare policy name as "invalid or does not exist", which
// reads like a missing resource and sends authors hunting for a policy that is
// in fact present on their account. The real cause is the shape of the value,
// so name that instead of letting the deploy fail on a misleading message.
//
// A value that still carries an unexpanded ${VAR} reference is passed over
// rather than judged. `azd ai agent init` deliberately writes ${RAI_POLICY_ID}
// instead of the resource ID so a project can be copied to another
// subscription unchanged, and the concrete ID is substituted from the azd
// environment at deploy time. This function also runs when the manifest is
// first read, before that substitution has happened, where the eventual shape
// is not knowable. The expanded value is re-validated on the deploy path, so
// deferring here does not let a malformed ID through.
func ValidateRaiPolicyName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	if strings.Contains(trimmed, "${") {
		return nil
	}
	if _, ok := azure.ParseRaiPolicyResourceID(trimmed); ok {
		return nil
	}
	return fmt.Errorf(
		"rai_policy_name %q must be the policy's full ARM resource ID, not its short name; "+
			"expected the form /subscriptions/<sub>/resourceGroups/<rg>/providers/"+
			"Microsoft.CognitiveServices/accounts/<account>/raiPolicies/<policy>",
		trimmed,
	)
}

// ValidatePolicies rejects policy entries the service will refuse.
func (p PromptAgent) ValidatePolicies() error {
	for i, policy := range p.Policies {
		if policy.Type != PolicyTypeRai {
			continue
		}
		if err := ValidateRaiPolicyName(policy.RaiPolicyName); err != nil {
			return fmt.Errorf("policies[%d]: %w", i, err)
		}
	}
	return nil
}

// ValidateHarness accepts open-ended harness names so newer service harnesses
// do not require an azd release.
func (p PromptAgent) ValidateHarness() error {
	return nil
}
