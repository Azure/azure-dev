// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"testing"
)

// TestArrayInput_BasicSerialization tests basic JSON serialization
func TestArrayInput_BasicSerialization(t *testing.T) {
	// Test that we can create and marshal a ArrayInput
	obj := &ArrayInput{}
	
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("Failed to marshal ArrayInput: %v", err)
	}
	
	var obj2 ArrayInput
	if err := json.Unmarshal(data, &obj2); err != nil {
		t.Fatalf("Failed to unmarshal ArrayInput: %v", err)
	}
}