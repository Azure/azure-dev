// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"testing"
)

// TestArrayProperty_BasicSerialization tests basic JSON serialization
func TestArrayProperty_BasicSerialization(t *testing.T) {
	// Test that we can create and marshal a ArrayProperty
	obj := &ArrayProperty{}

	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("Failed to marshal ArrayProperty: %v", err)
	}

	var obj2 ArrayProperty
	if err := json.Unmarshal(data, &obj2); err != nil {
		t.Fatalf("Failed to unmarshal ArrayProperty: %v", err)
	}
}
