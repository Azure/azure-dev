// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"strings"
	"testing"
)

// A Foundry endpoint in a sovereign cloud is a correct URL this extension
// cannot use, and those are two different failures.
//
// It used to be reported as "not a recognized Foundry host", which reads as a
// typo. A reader with a working Government project would go and check a URL
// that was already right, and find nothing wrong with it, because nothing is.
func TestSovereignEndpointIsRefusedByName(t *testing.T) {
	clouds := map[string]string{
		"https://contoso.services.ai.azure.us/api/projects/p": "Azure Government",
		"https://contoso.services.ai.azure.cn/api/projects/p": "China",
	}

	for endpoint, cloud := range clouds {
		t.Run(cloud, func(t *testing.T) {
			_, _, err := Validate(endpoint)

			if err == nil {
				t.Fatal("accepted an endpoint whose token audience is the wrong cloud")
			}
			if !strings.Contains(err.Error(), cloud) {
				t.Errorf("the refusal does not name the cloud: %v", err)
			}
			if strings.Contains(err.Error(), "not a recognized Foundry host") {
				t.Errorf("still reported as a malformed host, which it is not: %v", err)
			}
		})
	}
}

// A host that really is nothing to do with Foundry still gets the original
// message. Widening the sovereign branch to cover typos would tell a reader
// their mistake is an unsupported cloud.
func TestAnUnrelatedHostIsStillJustWrong(t *testing.T) {
	_, _, err := Validate("https://contoso.example.com/api/projects/p")

	if err == nil {
		t.Fatal("accepted a host that is not a Foundry endpoint at all")
	}
	if strings.Contains(err.Error(), "does not support yet") {
		t.Errorf("a typo was reported as an unsupported cloud: %v", err)
	}
}

// The public host keeps working, which is the case every reader is in.
func TestPublicEndpointStillValidates(t *testing.T) {
	normalized, _, err := Validate("https://contoso.services.ai.azure.com/api/projects/p")

	if err != nil {
		t.Fatalf("a public Foundry endpoint was refused: %v", err)
	}
	if normalized == "" {
		t.Error("validated but returned nothing to use")
	}
}
