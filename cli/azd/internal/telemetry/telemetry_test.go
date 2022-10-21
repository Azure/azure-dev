package telemetry

import (
	"context"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	appinsightsexporter "github.com/azure/azure-dev/cli/azd/internal/telemetry/appinsights-exporter"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetTelemetrySystem(t *testing.T) {
	devEndpointConfig, err := appinsightsexporter.NewEndpointConfig(devConnectionString)
	require.NoError(t, err)
	prodEndpointConfig, err := appinsightsexporter.NewEndpointConfig(prodConnectionString)
	require.NoError(t, err)

	type args struct {
		version                     string
		disableTelemetryEnvVarValue string
	}
	tests := []struct {
		name         string
		args         args
		expectNil    bool
		expectConfig appinsightsexporter.EndpointConfig
	}{
		{
			"DevVersion",
			args{"0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)", "unset"},
			false,
			devEndpointConfig,
		},
		{
			"DevVersionTelemetryEnabled",
			args{"0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)", "yes"},
			false,
			devEndpointConfig,
		},
		{
			"DevVersionTelemetryDisabled",
			args{"0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)", "no"},
			true,
			devEndpointConfig,
		},

		{"ProdVersion", args{"1.0.0 (commit 13ec2b11aa755b11640fa16b8664cb8741d5d300)", "no"}, true, prodEndpointConfig},
		{"ProdVersion", args{"1.0.0 (commit 13ec2b11aa755b11640fa16b8664cb8741d5d300)", "unset"}, false, prodEndpointConfig},
		{"ProdVersion", args{"1.0.0 (commit 13ec2b11aa755b11640fa16b8664cb8741d5d300)", "yes"}, false, prodEndpointConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := internal.Version
			defer func() { internal.Version = orig }()
			internal.Version = tt.args.version

			if tt.args.disableTelemetryEnvVarValue == "unset" {
				ostest.Unsetenv(t, collectTelemetryEnvVar)
			} else {
				ostest.Setenv(t, collectTelemetryEnvVar, tt.args.disableTelemetryEnvVarValue)
			}

			ts := GetTelemetrySystem()

			if tt.expectNil {
				assert.Nil(t, ts)
			} else {
				require.NotNil(t, ts)
				assert.Equal(t, tt.expectConfig, ts.config)
				assert.NotNil(t, ts.GetTelemetryQueue())
				assert.NotNil(t, ts.NewUploader(true))

				err := ts.Shutdown(context.Background())
				assert.NoError(t, err)
			}
			once = sync.Once{}
		})
	}
}

func TestTelemetrySystem_RunBackgroundUpload(t *testing.T) {
	type args struct {
		ctx                context.Context
		enableDebugLogging bool
	}
	ts := GetTelemetrySystem()
	require.NotNil(t, ts)

	tests := []struct {
		name                     string
		simulateUploadInProgress bool
		args                     args
	}{
		{"Debug", false, args{context.Background(), true}},
		{"NonDebug", false, args{context.Background(), false}},
		{"UploadInProgress", true, args{context.Background(), false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.simulateUploadInProgress {
				fl, locked, err := ts.tryUploadLock()
				assert.NoError(t, err)
				assert.True(t, locked)
				defer func() { require.NoError(t, fl.Unlock()) }()
			}

			err := ts.RunBackgroundUpload(tt.args.ctx, tt.args.enableDebugLogging)
			assert.NoError(t, err)
		})
	}
}
