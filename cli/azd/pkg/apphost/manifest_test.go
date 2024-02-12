package apphost

import (
	"context"
	"reflect"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/stretchr/testify/require"
)

func BenchmarkManifestFromAppHost(b *testing.B) {
	const appHostProject = "../../test/functional/testdata/samples/aspire-full/AspireAzdTests.AppHost"

	for i := 0; i < b.N; i++ {
		_, err := ManifestFromAppHost(
			context.Background(),
			appHostProject,
			dotnet.NewDotNetCli(exec.NewCommandRunner(nil)))
		require.NoError(b, err)
	}
}

func TestManifestFromAppHost(t *testing.T) {
	type args struct {
		ctx            context.Context
		appHostProject string
		dotnetCli      dotnet.DotNetCli
	}
	tests := []struct {
		name    string
		args    args
		want    *Manifest
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ManifestFromAppHost(tt.args.ctx, tt.args.appHostProject, tt.args.dotnetCli)
			if (err != nil) != tt.wantErr {
				t.Errorf("ManifestFromAppHost() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ManifestFromAppHost() = %v, want %v", got, tt.want)
			}
		})
	}
}
