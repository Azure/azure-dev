// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"net/http"
	"testing"

	"azure.ai.projects/internal/pkg/recordproxy"

	"github.com/stretchr/testify/require"
)

func TestNewArmClientOptionsUsesRecordTransport(t *testing.T) {
	transport := &http.Transport{}
	previous := recordproxy.Transport
	recordproxy.Transport = transport
	t.Cleanup(func() {
		recordproxy.Transport = previous
	})

	options := NewArmClientOptions()

	client, ok := options.ClientOptions.Transport.(*http.Client)
	require.True(t, ok)
	require.Same(t, transport, client.Transport)
}
