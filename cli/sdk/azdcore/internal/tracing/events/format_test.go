// Copyright (c) Microsoft Corporation. All rights reserved.

// Licensed under the MIT License.

package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetCommandEventName(t *testing.T) {
	type args struct {
		cmdPath string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"Single", args{"azd provision"}, "cmd.provision"},
		{"Multiple", args{"azd env list"}, "cmd.env.list"},
		{"SpecialChar", args{"azd env get-values"}, "cmd.env.get-values"},

		// These cases should not happen in the actual application.
		// However, we should be lenient in formatting these and not error.
		{"LenientSingle", args{"provision"}, "cmd.provision"},
		{"LenientMultiple", args{"env list"}, "cmd.env.list"},
		{"LenientSpecialChar", args{"env get-values"}, "cmd.env.get-values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventName := GetCommandEventName(tt.args.cmdPath)
			assert.Equal(t, eventName, tt.want)
		})
	}
}
