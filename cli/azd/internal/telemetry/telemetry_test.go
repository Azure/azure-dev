package telemetry

import (
	"context"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetTelemetrySystem(t *testing.T) {
	tests := []struct {
		name               string
		azdVersion         string
		expectNil          bool
		instrumentationKey string
	}{
		{"DevVersion", "0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)", false, devInstrumentationKey},
		{"ProdVersion", "1.0.0 (commit 13ec2b11aa755b11640fa16b8664cb8741d5d300)", true, prodInstrumentationKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := internal.Version
			defer func() { internal.Version = orig }()
			internal.Version = tt.azdVersion

			ts := GetTelemetrySystem()

			if tt.expectNil {
				assert.Nil(t, ts)
			} else {
				require.NotNil(t, ts)
				assert.Equal(t, tt.instrumentationKey, ts.instrumentationKey)
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
