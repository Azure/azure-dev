// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

func (s *silentSuccessClient) AcquireTokenSilent(
	_ context.Context,
	_ []string,
	_ ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	return s.result, nil
}

func (s *silentErrorClient) AcquireTokenSilent(
	_ context.Context,
	_ []string,
	_ ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	return public.AuthResult{}, s.err
}

// mockPublicClientFull supports all publicClient methods with
// configurable results for Accounts / AcquireTokenInteractive /
// AcquireTokenByDeviceCode / AcquireTokenSilent / RemoveAccount.
type mockPublicClientFull struct {
	accounts         []public.Account
	accountsErr      error
	interactiveRes   public.AuthResult
	interactiveErr   error
	deviceCodeResult deviceCodeResult
	deviceCodeErr    error
	silentRes        public.AuthResult
	silentErr        error
	removeErr        error
}

func (m *mockPublicClientFull) Accounts(
	_ context.Context,
) ([]public.Account, error) {
	return m.accounts, m.accountsErr
}

func (m *mockPublicClientFull) RemoveAccount(
	_ context.Context, _ public.Account,
) error {
	return m.removeErr
}

func (m *mockPublicClientFull) AcquireTokenInteractive(
	_ context.Context, _ []string,
	_ ...public.AcquireInteractiveOption,
) (public.AuthResult, error) {
	return m.interactiveRes, m.interactiveErr
}

func (m *mockPublicClientFull) AcquireTokenByDeviceCode(
	_ context.Context, _ []string,
	_ ...public.AcquireByDeviceCodeOption,
) (deviceCodeResult, error) {
	return m.deviceCodeResult, m.deviceCodeErr
}

func (m *mockPublicClientFull) AcquireTokenSilent(
	_ context.Context, _ []string,
	_ ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	return m.silentRes, m.silentErr
}

func (s *stubDeviceCode) Message() string { return s.msg }

func (s *stubDeviceCode) UserCode() string { return s.code }

func (s *stubDeviceCode) AuthenticationResult(
	_ context.Context,
) (public.AuthResult, error) {
	return s.res, s.err
}
