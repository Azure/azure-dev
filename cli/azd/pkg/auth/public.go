// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

// publicClient looks like a subset of the public.Client surface area, with small tweaks, to aid testing.
type publicClient interface {
	Accounts(ctx context.Context) ([]public.Account, error)
	RemoveAccount(ctx context.Context, account public.Account) error
	AcquireTokenInteractive(context.Context, []string, ...public.AcquireInteractiveOption) (public.AuthResult, error)
	AcquireTokenByDeviceCode(context.Context, []string, ...public.AcquireByDeviceCodeOption) (deviceCodeResult, error)
	AcquireTokenSilent(context.Context, []string, ...public.AcquireSilentOption) (public.AuthResult, error)
}

type deviceCodeResult interface {
	Message() string
	UserCode() string
	AuthenticationResult(context.Context) (public.AuthResult, error)
}

type msalPublicClientAdapter struct {
	client *public.Client
}

func (m *msalPublicClientAdapter) Accounts(ctx context.Context) ([]public.Account, error) {
	return m.client.Accounts(ctx)
}

func (m *msalPublicClientAdapter) RemoveAccount(ctx context.Context, account public.Account) error {
	return m.client.RemoveAccount(ctx, account)
}

func (m *msalPublicClientAdapter) AcquireTokenInteractive(
	ctx context.Context, scopes []string, options ...public.AcquireInteractiveOption,
) (public.AuthResult, error) {
	res, err := m.client.AcquireTokenInteractive(ctx, scopes, options...)
	if err != nil {
		return res, newAuthFailedErrorFromMsalErr(err)
	}

	return res, nil
}

func (m *msalPublicClientAdapter) AcquireTokenByDeviceCode(
	ctx context.Context, scopes []string, options ...public.AcquireByDeviceCodeOption) (deviceCodeResult, error) {
	code, err := m.client.AcquireTokenByDeviceCode(ctx, scopes, options...)
	if err != nil {
		return nil, newAuthFailedErrorFromMsalErr(err)
	}

	return &msalDeviceCodeAdapter{code: &code}, nil
}

func (m *msalPublicClientAdapter) AcquireTokenSilent(
	ctx context.Context, scopes []string, options ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	res, err := m.client.AcquireTokenSilent(ctx, scopes, options...)
	if err != nil {
		return res, newAuthFailedErrorFromMsalErr(err)
	}

	return res, nil
}

type msalDeviceCodeAdapter struct {
	code *public.DeviceCode
}

func (m *msalDeviceCodeAdapter) Message() string {
	return m.code.Result.Message
}

func (m *msalDeviceCodeAdapter) UserCode() string {
	return m.code.Result.UserCode
}

func (m *msalDeviceCodeAdapter) AuthenticationResult(ctx context.Context) (public.AuthResult, error) {
	res, err := m.code.AuthenticationResult(ctx)
	if err != nil {
		return res, newAuthFailedErrorFromMsalErr(err)
	}

	return res, nil
}
