// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

func TestLoggedIn(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		loginType   LoginType
		account     string
		wantText    string
		wantMessage string
	}{
		{
			name: "system-assigned managed identity", loginType: ClientIdLoginType,
			wantText: "Logged in to Azure", wantMessage: "Logged in to Azure\n",
		},
		{
			name: "user without account name", loginType: EmailLoginType,
			wantText: "Logged in to Azure", wantMessage: "Logged in to Azure\n",
		},
		{
			name: "user", loginType: EmailLoginType, account: "user@example.com",
			wantText:    "Logged in to Azure as " + output.WithBold("%s", "user@example.com"),
			wantMessage: "Logged in to Azure as user@example.com\n",
		},
		{
			name: "service principal", loginType: ClientIdLoginType, account: "client-id",
			wantText:    "Logged in to Azure as (" + output.WithGrayFormat("%s", "client-id") + ")",
			wantMessage: "Logged in to Azure as client-id\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			view := &LoggedIn{LoginType: tt.loginType, LoggedInAs: tt.account}
			require.Equal(t, "  "+tt.wantText, view.ToString("  "))

			data, err := json.Marshal(view)
			require.NoError(t, err)
			var event struct {
				Type      string                   `json:"type"`
				Timestamp time.Time                `json:"timestamp"`
				Data      contracts.ConsoleMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(data, &event))
			require.Equal(t, string(contracts.ConsoleMessageEventDataType), event.Type)
			require.False(t, event.Timestamp.IsZero())
			require.Equal(t, tt.wantMessage, event.Data.Message)
		})
	}
}
