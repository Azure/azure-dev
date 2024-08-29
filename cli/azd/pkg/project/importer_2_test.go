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
		{
			name:            "empty",
			input:           "",
			wantNames:       nil,
			wantExpressions: nil,
		},
		{
			name:            "no variables",
			input:           "foo",
			wantNames:       nil,
			wantExpressions: nil,
		},
		{
			name:            "one variable",
			input:           "${foo}",
			wantNames:       []string{"foo"},
			wantExpressions: []location{{0, 5}},
		},
		{
			name:            "two variables",
			input:           "${foo} ${bar}",
			wantNames:       []string{"foo", "bar"},
			wantExpressions: []location{{0, 5}, {7, 12}},
		},
		{
			name:            "two variables with text",
			input:           "${foo:=value} ${bar#subs}",
			wantNames:       []string{"foo", "bar"},
			wantExpressions: []location{{0, 12}, {14, 24}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNames, gotExpressions := parseEnvSubtVariables(tt.input)
			if !reflect.DeepEqual(gotNames, tt.wantNames) {
				t.Errorf("parseEnvSubtVariables() gotNames = %v, want %v", gotNames, tt.wantNames)
			}
			if !reflect.DeepEqual(gotExpressions, tt.wantExpressions) {
				t.Errorf("parseEnvSubtVariables() gotExpressions = %v, want %v", gotExpressions, tt.wantExpressions)
			}
		})
	}
}
