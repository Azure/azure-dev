// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

type authStatusTestManager struct {
	accountErr error
	credential azcore.TokenCredential
}

func (m authStatusTestManager) Mode() (auth.AuthSource, error) { return auth.AzdBuiltIn, nil }
func (m authStatusTestManager) LoginScopes() []string          { return []string{"test"} }
func (m authStatusTestManager) SetBuiltInAuthMode() error {
	return errors.New("unexpected auth mode change")
}

func (m authStatusTestManager) LogInDetails(context.Context) (*auth.LogInDetails, error) {
	if m.accountErr != nil {
		return nil, m.accountErr
	}
	return &auth.LogInDetails{LoginType: auth.EmailLoginType, Account: "user@example.test"}, nil
}

func (m authStatusTestManager) CredentialForCurrentUser(
	context.Context, *auth.CredentialForCurrentUserOptions,
) (azcore.TokenCredential, error) {
	return m.credential, nil
}

func TestAuthStatusJSONReasons(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		accountErr error
		tokenErr   error
		wantStatus string
		wantReason string
	}{
		{
			name: "missing account", accountErr: fmt.Errorf("read account: %w", auth.ErrNoCurrentUser),
			wantStatus: "unauthenticated", wantReason: "not_logged_in",
		},
		{
			name: "account read failed", accountErr: errors.New("account cache unreadable"),
			wantStatus: "unauthenticated", wantReason: "verification_failed",
		},
		{
			name: "token needs renewal", tokenErr: fmt.Errorf("verify account: %w", &auth.ReLoginRequiredError{}),
			wantStatus: "unauthenticated", wantReason: "login_required",
		},
		{
			name: "token verification failed", tokenErr: errors.New("identity endpoint unavailable"),
			wantStatus: "unauthenticated", wantReason: "verification_failed",
		},
		{name: "authenticated", wantStatus: "authenticated"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			verified := false
			action := &authStatusAction{
				formatter: &output.JsonFormatter{},
				writer:    &stdout,
				console:   mockinput.NewMockConsole(),
				authManager: authStatusTestManager{
					accountErr: tt.accountErr,
					credential: authTokenFn(func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
						verified = true
						return azcore.AccessToken{ExpiresOn: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}, tt.tokenErr
					}),
				},
				flags: &authStatusFlags{global: &internal.GlobalCommandOptions{NoPrompt: true}},
			}
			result, err := action.Run(t.Context())
			require.NoError(t, err, "JSON output does not signal unauthenticated via exit status")
			require.Nil(t, result)
			require.Equal(t, tt.accountErr == nil, verified)

			var got map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
			require.JSONEq(t, fmt.Sprintf("%q", tt.wantStatus), string(got["status"]))
			if tt.wantReason == "" {
				require.NotContains(t, got, "reason")
			} else {
				require.JSONEq(t, fmt.Sprintf("%q", tt.wantReason), string(got["reason"]))
			}
		})
	}
}

func Test_NewAuthStatusAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &authStatusFlags{}
	formatter := &output.JsonFormatter{}
	console := mockinput.NewMockConsole()
	a := newAuthStatusAction(formatter, io.Discard, nil, flags, console)
	sa := a.(*authStatusAction)
	require.Same(t, flags, sa.flags)
	require.Same(t, formatter, sa.formatter)
}

func Test_NewAuthStatusAction(t *testing.T) {
	t.Parallel()
	action := newAuthStatusAction(
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // authManager
		&authStatusFlags{},
		mockinput.NewMockConsole(),
	)
	require.NotNil(t, action)
}

func Test_NewAuthStatusCmd(t *testing.T) {
	t.Parallel()
	cmd := newAuthStatusCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "status", cmd.Use)
}

func Test_NewAuthStatusFlags(t *testing.T) {
	t.Parallel()
	cmd := newAuthStatusCmd()
	global := &internal.GlobalCommandOptions{}
	flags := newAuthStatusFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
}

func Test_NewAuthStatusFlags_FC(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newAuthStatusFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewAuthStatusCmd_FC(t *testing.T) {
	t.Parallel()
	cmd := newAuthStatusCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "status")
}
