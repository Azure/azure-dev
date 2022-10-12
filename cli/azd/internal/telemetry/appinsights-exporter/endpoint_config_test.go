package appinsightsexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewEndpointConfig(t *testing.T) {
	type args struct {
		connectionString string
	}
	tests := []struct {
		name    string
		args    args
		want    EndpointConfig
		wantErr bool
	}{
		// Valid cases
		{
			"IKeyOnly",
			args{"InstrumentationKey=foobar"},
			EndpointConfig{InstrumentationKey: "foobar", EndpointUrl: "https://dc.services.visualstudio.com/v2/track"},
			false,
		},
		{
			"EndpointSuffix",
			args{"InstrumentationKey=foobar;EndpointSuffix=localhost:1010"},
			EndpointConfig{InstrumentationKey: "foobar", EndpointUrl: "https://dc.localhost:1010/v2/track"},
			false,
		},
		{
			"ExplicitEndpoint",
			args{"InstrumentationKey=foobar;IngestionEndpoint=https://localhost:1030"},
			EndpointConfig{InstrumentationKey: "foobar", EndpointUrl: "https://localhost:1030/v2/track"},
			false,
		},
		{
			"ExplicitEndpointOverride",
			args{"InstrumentationKey=foobar;IngestionEndpoint=https://localhost:1030;EndpointSuffix=localhost:1010"},
			EndpointConfig{InstrumentationKey: "foobar", EndpointUrl: "https://localhost:1030/v2/track"},
			false,
		},

		// Cases where we reformat input for user-friendly handling
		{
			"ExplicitEndpointTrailingSlash",
			args{"InstrumentationKey=foobar;IngestionEndpoint=https://localhost:1030//"},
			EndpointConfig{InstrumentationKey: "foobar", EndpointUrl: "https://localhost:1030/v2/track"},
			false,
		},
		{
			"EndpointSuffixLeadingDot",
			args{"InstrumentationKey=foobar;EndpointSuffix=..localhost:1010"},
			EndpointConfig{InstrumentationKey: "foobar", EndpointUrl: "https://dc.localhost:1010/v2/track"},
			false,
		},
		{
			"EndpointSuffixTrailingSlash",
			args{"InstrumentationKey=foobar;EndpointSuffix=localhost:1010//"},
			EndpointConfig{InstrumentationKey: "foobar", EndpointUrl: "https://dc.localhost:1010/v2/track"},
			false,
		},

		// Invalid cases
		{"Empty", args{""}, EndpointConfig{}, true},
		{"InvalidMissingIKey", args{"IngestionEndpoint=https://localhost:1030"}, EndpointConfig{}, true},
		{"InvalidSettingKey", args{"InstrumentationKey=foobar;=Invalid"}, EndpointConfig{}, true},
		{"InvalidSettingValue", args{"InstrumentationKey=foobar;Invalid"}, EndpointConfig{}, true},
		{"InvalidSettingSeparator", args{"InstrumentationKey=foobar;;"}, EndpointConfig{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := NewEndpointConfig(tt.args.connectionString)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, config)
			}
		})
	}
}
