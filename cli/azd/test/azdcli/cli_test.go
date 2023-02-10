// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

// Contains support for automating the use of the azd CLI

package azdcli

import (
	"context"
	"reflect"
	"testing"
)

func TestCLI_RunCommandWithStdIn(t *testing.T) {
	type args struct {
		ctx   context.Context
		stdin string
		args  []string
	}
	tests := []struct {
		name    string
		cli     *CLI
		args    args
		want    *CliResult
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cli.RunCommandWithStdIn(tt.args.ctx, tt.args.stdin, tt.args.args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("CLI.RunCommandWithStdIn() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CLI.RunCommandWithStdIn() = %v, want %v", got, tt.want)
			}
		})
	}
}
