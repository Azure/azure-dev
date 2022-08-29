package commands

import (
	"context"
	"reflect"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestBuild(t *testing.T) {
	testAction := ActionFunc(
		func(context.Context, *cobra.Command, []string, *azdcontext.AzdContext) error {
			return nil
		},
	)

	rootOptions := &internal.GlobalCommandOptions{
		EnvironmentName:    "test",
		EnableDebugLogging: false,
		EnableTelemetry:    true,
	}

	type args struct {
		use          string
		short        string
		buildOptions *BuildOptions
	}
	tests := []struct {
		name string
		args args
		want *cobra.Command
	}{
		{name: "RequiredOnly",
			args: args{
				"basic",
				"basic-short",
				nil,
			},
		},
		{name: "Extended",
			args: args{
				"ext",
				"ext-short",
				&BuildOptions{
					Long:    "lorem",
					Aliases: []string{"alias1", "alias2"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := Build(testAction, rootOptions, tt.args.use, tt.args.short, tt.args.buildOptions)

			assert.Equal(t, cmd.Short, tt.args.short)
			assert.Equal(t, cmd.Use, tt.args.use)

			if tt.args.buildOptions != nil {
				assert.Equal(t, cmd.Long, tt.args.buildOptions.Long)
				assert.Equal(t, cmd.Aliases, tt.args.buildOptions.Aliases)
			}
		})
	}
}

func Test_createRootContext(t *testing.T) {
	type args struct {
		ctx         context.Context
		cmd         *cobra.Command
		rootOptions *internal.GlobalCommandOptions
	}
	tests := []struct {
		name    string
		args    args
		want    context.Context
		want1   *azdcontext.AzdContext
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := createRootContext(tt.args.ctx, tt.args.cmd, tt.args.rootOptions)
			if (err != nil) != tt.wantErr {
				t.Errorf("createRootContext() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("createRootContext() got = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(got1, tt.want1) {
				t.Errorf("createRootContext() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}
