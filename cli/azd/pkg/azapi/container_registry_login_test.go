// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/require"
)

// setupLoginTest creates a containerRegistryService with mocked
// dependencies and returns atomic counters tracking how many times
// the ACR token exchange and docker login were invoked.
func setupLoginTest(t *testing.T) (
	*containerRegistryService,
	*mocks.MockContext,
	*atomic.Int32,
	*atomic.Int32,
) {
	t.Helper()
	mockCtx := mocks.NewMockContext(t.Context())

	var credCalls, dockerCalls atomic.Int32

	// Mock ACR token exchange (Credentials → getAcrToken path).
	mockCtx.HttpClient.When(func(r *http.Request) bool {
		return r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "oauth2/exchange")
	}).RespondFn(func(r *http.Request) (*http.Response, error) {
		credCalls.Add(1)
		return acrTokenResponse(r)
	})

	// Mock docker login command.
	mockCtx.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
		return strings.Contains(cmd, "docker login")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		dockerCalls.Add(1)
		return exec.RunResult{}, nil
	})

	svc := newLoginTestService(t, mockCtx)
	return svc, mockCtx, &credCalls, &dockerCalls
}

// newLoginTestService builds a *containerRegistryService backed by
// the provided MockContext's HTTP transport and command runner.
func newLoginTestService(
	t *testing.T, mc *mocks.MockContext,
) *containerRegistryService {
	t.Helper()
	svc := NewContainerRegistryService(
		mockaccount.SubscriptionCredentialProviderFunc(
			func(_ context.Context, _ string) (azcore.TokenCredential, error) {
				return mc.Credentials, nil
			}),
		docker.NewCli(mc.CommandRunner),
		mc.ArmClientOptions,
		mc.CoreClientOptions,
	)
	return svc.(*containerRegistryService)
}

// acrTokenResponse returns a minimal successful ACR token exchange.
func acrTokenResponse(
	r *http.Request,
) (*http.Response, error) {
	body := struct {
		RefreshToken string `json:"refresh_token"`
	}{RefreshToken: "mock-refresh-token"}
	return mocks.CreateHttpResponseWithBody(
		r, http.StatusOK, body,
	)
}

func TestLogin(t *testing.T) {
	t.Run("SingleLoginSucceeds", func(t *testing.T) {
		svc, _, credCalls, dockerCalls := setupLoginTest(t)

		err := svc.Login(t.Context(), "sub1", "reg.azurecr.io")

		require.NoError(t, err)
		require.Equal(t, int32(1), credCalls.Load())
		require.Equal(t, int32(1), dockerCalls.Load())
	})

	t.Run("CacheHitSkipsLogin", func(t *testing.T) {
		svc, _, credCalls, dockerCalls := setupLoginTest(t)
		svc.loginDone.Store("sub1:reg.azurecr.io", true)

		err := svc.Login(t.Context(), "sub1", "reg.azurecr.io")

		require.NoError(t, err)
		require.Equal(t, int32(0), credCalls.Load(),
			"credentials should not be fetched for cached login")
		require.Equal(t, int32(0), dockerCalls.Load(),
			"docker login should not be called for cached login")
	})

	t.Run("ConcurrentCallersDeduplicate", func(t *testing.T) {
		svc, _, credCalls, dockerCalls := setupLoginTest(t)

		const n = 10
		errs := make([]error, n)
		var barrier, done sync.WaitGroup
		barrier.Add(n)
		done.Add(n)

		for i := range n {
			go func() {
				defer done.Done()
				barrier.Done()
				barrier.Wait() // all goroutines start ~simultaneously
				errs[i] = svc.Login(
					t.Context(), "sub1", "reg.azurecr.io",
				)
			}()
		}

		done.Wait()

		for i, err := range errs {
			require.NoError(t, err, "caller %d should succeed", i)
		}
		require.Equal(t, int32(1), credCalls.Load(),
			"credentials should be fetched exactly once")
		require.Equal(t, int32(1), dockerCalls.Load(),
			"docker login should be called exactly once")
	})

	t.Run("CancelledContextDoesNotFailOthers", func(t *testing.T) {
		mc := mocks.NewMockContext(t.Context())
		gate := make(chan struct{})
		entered := make(chan struct{}, 1)

		// HTTP mock blocks until the test releases the gate.
		mc.HttpClient.When(func(r *http.Request) bool {
			return r.Method == http.MethodPost &&
				strings.Contains(r.URL.Path, "oauth2/exchange")
		}).RespondFn(func(r *http.Request) (*http.Response, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-gate
			return acrTokenResponse(r)
		})

		mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "docker login")
		}).Respond(exec.RunResult{})

		svc := newLoginTestService(t, mc)
		cancelCtx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var err1, err2 error
		var g1, g2 sync.WaitGroup
		g1.Add(1)
		g2.Add(1)

		go func() {
			defer g1.Done()
			err1 = svc.Login(
				cancelCtx, "sub1", "reg.azurecr.io",
			)
		}()
		go func() {
			defer g2.Done()
			err2 = svc.Login(
				t.Context(), "sub1", "reg.azurecr.io",
			)
		}()

		// Wait for the singleflight function to start, then cancel
		// the first caller. Because Login uses WithoutCancel, the
		// shared work continues for the second caller.
		<-entered
		cancel()
		g1.Wait()   // cancelled caller must have returned
		close(gate) // release the shared work
		g2.Wait()

		require.ErrorIs(t, err1, context.Canceled)
		require.NoError(t, err2)

		// The work completed despite the cancellation.
		_, ok := svc.loginDone.Load("sub1:reg.azurecr.io")
		require.True(t, ok,
			"login should complete despite caller cancellation")
	})

	t.Run("LoginDonePopulatedAfterSuccess", func(t *testing.T) {
		svc, _, _, _ := setupLoginTest(t)

		err := svc.Login(t.Context(), "sub1", "reg.azurecr.io")
		require.NoError(t, err)

		val, ok := svc.loginDone.Load("sub1:reg.azurecr.io")
		require.True(t, ok,
			"loginDone should have entry after successful login")
		require.Equal(t, true, val)
	})

	t.Run("LoginErrorPropagated", func(t *testing.T) {
		mc := mocks.NewMockContext(t.Context())

		mc.HttpClient.When(func(r *http.Request) bool {
			return r.Method == http.MethodPost &&
				strings.Contains(r.URL.Path, "oauth2/exchange")
		}).RespondFn(func(r *http.Request) (*http.Response, error) {
			return acrTokenResponse(r)
		})

		mc.CommandRunner.When(func(_ exec.RunArgs, cmd string) bool {
			return strings.Contains(cmd, "docker login")
		}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{},
				fmt.Errorf("docker daemon not running")
		})

		svc := newLoginTestService(t, mc)

		err := svc.Login(t.Context(), "sub1", "reg.azurecr.io")

		require.Error(t, err)
		require.Contains(t, err.Error(),
			"failed logging into docker registry")
		require.Contains(t, err.Error(),
			"docker daemon not running")

		// loginDone must NOT be populated on failure.
		_, ok := svc.loginDone.Load("sub1:reg.azurecr.io")
		require.False(t, ok,
			"loginDone should not have entry after failed login")
	})

	t.Run("DifferentRegistriesDontShareCache", func(t *testing.T) {
		svc, _, credCalls, dockerCalls := setupLoginTest(t)

		err := svc.Login(
			t.Context(), "sub1", "registryA.azurecr.io",
		)
		require.NoError(t, err)
		require.Equal(t, int32(1), credCalls.Load())

		err = svc.Login(
			t.Context(), "sub1", "registryB.azurecr.io",
		)
		require.NoError(t, err)
		require.Equal(t, int32(2), credCalls.Load(),
			"second registry should trigger new credential fetch")
		require.Equal(t, int32(2), dockerCalls.Load(),
			"second registry should trigger new docker login")
	})
}
