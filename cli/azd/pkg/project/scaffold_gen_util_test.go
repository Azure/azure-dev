// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"reflect"
	"testing"
)

func Test_parseEnvSubtVariables(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		wantNames       []string
		wantExpressions []location
	}{
		{"empty", "", nil, nil},
		{"no variables", "foo", nil, nil},
		{"one variable", "${foo}", []string{"foo"}, []location{{0, 5}}},
		{"two variables", "${foo} ${bar}", []string{"foo", "bar"}, []location{{0, 5}, {7, 12}}},
		{"two variables with text", "${foo:=value} ${bar#subs}", []string{"foo", "bar"}, []location{{0, 12}, {14, 24}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNames, gotExpressions := parseEnvSubstVariables(tt.input)
			if !reflect.DeepEqual(gotNames, tt.wantNames) {
				t.Errorf("parseEnvSubtVariables() gotNames = %v, want %v", gotNames, tt.wantNames)
			}
			if !reflect.DeepEqual(gotExpressions, tt.wantExpressions) {
				t.Errorf("parseEnvSubtVariables() gotExpressions = %v, want %v", gotExpressions, tt.wantExpressions)
			}
		})
	}
}
