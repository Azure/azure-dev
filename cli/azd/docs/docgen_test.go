// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import "testing"

func Test_formatCommandLine(t *testing.T) {
	type args struct {
		line string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "No dollar sign",
			args: args{line: "hello world"},
			want: "hello world",
		},
		{
			name: "No dollar sign",
			args: args{line: "hello world"},
			want: "hello world",
		},
		{
			name: "Dollar sign but not command",
			args: args{line: "hello $ world"},
			want: "hello $ world",
		},
		{
			name: "Command without leading spaces",
			args: args{line: "$ command --with someParameters"},
			want: "command --with someParameters",
		},
		{
			name: "Command with leading spaces",
			args: args{line: "  $ command --with someParameters"},
			want: "  command --with someParameters",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCommandLine(tt.args.line); got != tt.want {
				t.Errorf("formatCommandLine() = %v, want %v", got, tt.want)
			}
		})
	}
}
