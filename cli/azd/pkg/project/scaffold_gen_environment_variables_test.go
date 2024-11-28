package project

import (
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestMergeEnvWithDuplicationCheck(t *testing.T) {
	var empty []scaffold.Env
	name1Value1 := []scaffold.Env{
		{
			Name:  "name1",
			Value: "value1",
		},
	}
	name1Value2 := []scaffold.Env{
		{
			Name:  "name1",
			Value: "value2",
		},
	}
	name2Value2 := []scaffold.Env{
		{
			Name:  "name2",
			Value: "value2",
		},
	}
	name1Value1Name2Value2 := []scaffold.Env{
		{
			Name:  "name1",
			Value: "value1",
		},
		{
			Name:  "name2",
			Value: "value2",
		},
	}

	tests := []struct {
		name      string
		a         []scaffold.Env
		b         []scaffold.Env
		wantEnv   []scaffold.Env
		wantError error
	}{
		{
			name:      "2 empty array",
			a:         empty,
			b:         empty,
			wantEnv:   empty,
			wantError: nil,
		},
		{
			name:      "one is empty, another is not",
			a:         empty,
			b:         name1Value1,
			wantEnv:   name1Value1,
			wantError: nil,
		},
		{
			name:      "no duplication",
			a:         name1Value1,
			b:         name2Value2,
			wantEnv:   name1Value1Name2Value2,
			wantError: nil,
		},
		{
			name:      "duplicated name but same value",
			a:         name1Value1,
			b:         name1Value1,
			wantEnv:   name1Value1,
			wantError: nil,
		},
		{
			name:    "duplicated name, different value",
			a:       name1Value1,
			b:       name1Value2,
			wantEnv: []scaffold.Env{},
			wantError: fmt.Errorf("duplicated environment variable. existingValue = %s, newValue = %s",
				name1Value1[0], name1Value2[0]),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := mergeEnvWithDuplicationCheck(tt.a, tt.b)
			assert.Equal(t, tt.wantEnv, env)
			assert.Equal(t, tt.wantError, err)
		})
	}
}
