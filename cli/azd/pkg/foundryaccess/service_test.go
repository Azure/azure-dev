// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundryaccess

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testProjectID = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/" +
	"providers/Microsoft.CognitiveServices/accounts/arm-account/projects/project"

var testTarget = Target{
	ProjectID: testProjectID,
	Endpoint:  "https://custom-subdomain.services.ai.azure.com/api/projects/project",
}

type mockBackend struct{ mock.Mock }

func (b *mockBackend) Resolve(ctx context.Context, target Target) (Project, error) {
	args := b.Called(ctx, target)
	return args.Get(0).(Project), args.Error(1)
}

func (b *mockBackend) EnsureGrant(ctx context.Context, project Project) (bool, error) {
	args := b.Called(ctx, project)
	return args.Bool(0), args.Error(1)
}

func TestEnsureAccessPolicy(t *testing.T) {
	probeFailure := errors.New("network restriction, not an RBAC denial")
	grantFailure := errors.New("assignment not authorized")
	for _, tt := range []struct {
		name       string
		policy     GrantPolicy
		states     []State
		probeError error
		grantError error
		grant      bool
		created    bool
		want       State
	}{
		{name: "inherited or custom access", policy: GrantFoundryUser, states: []State{Allowed}, want: Allowed},
		{name: "default read only", states: []State{Denied}, want: Denied},
		{name: "explicit read only", policy: CheckOnly, states: []State{Denied}, want: Denied},
		{name: "unknown never grants", policy: GrantFoundryUser, states: []State{Unknown}, want: Unknown},
		{name: "probe failure never grants", policy: GrantFoundryUser, probeError: probeFailure, want: Unknown},
		{name: "invalid probe state", policy: GrantFoundryUser, states: []State{"invalid"}, want: Unknown},
		{name: "created", policy: GrantFoundryUser, states: []State{Denied, Allowed},
			grant: true, created: true, want: Allowed},
		{name: "concurrent existing", policy: GrantFoundryUser, states: []State{Denied, Allowed},
			grant: true, want: Allowed},
		{name: "grant denied", policy: GrantFoundryUser, states: []State{Denied},
			grant: true, grantError: grantFailure, want: Denied},
		{name: "propagation becomes unknown", policy: GrantFoundryUser, states: []State{Denied, Unknown},
			grant: true, created: true, want: Unknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backend := &mockBackend{}
			project := Project{Target: testTarget}
			backend.On("Resolve", mock.Anything, testTarget).Return(project, nil).Once()
			if tt.grant {
				backend.On("EnsureGrant", mock.Anything, project).Return(tt.created, tt.grantError).Once()
			}
			service, err := NewService(backend, nil)
			require.NoError(t, err)
			calls := 0
			result, err := service.Ensure(t.Context(), Request{Target: testTarget, Operation: "toolboxes.write",
				Policy: tt.policy, Probe: func(_ context.Context, actual Project) (State, error) {
					require.Equal(t, project, actual)
					calls++
					if tt.probeError != nil {
						return Denied, tt.probeError // an error must override even a Denied state
					}
					require.LessOrEqual(t, calls, len(tt.states))
					return tt.states[calls-1], nil
				}})
			if tt.probeError != nil {
				require.ErrorIs(t, err, tt.probeError)
			} else if tt.grantError != nil {
				require.ErrorIs(t, err, tt.grantError)
			} else if tt.name == "invalid probe state" {
				require.ErrorContains(t, err, "invalid state")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.want, result.State)
			require.Equal(t, tt.grant && tt.grantError == nil, result.GrantEnsured)
			require.Equal(t, tt.created, result.AssignmentCreated)
			require.Equal(t, calls, result.ProbeAttempts)
			backend.AssertExpectations(t)
			if !tt.grant {
				backend.AssertNotCalled(t, "EnsureGrant", mock.Anything, mock.Anything)
			}
		})
	}
}

func TestEnsurePropagation(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "eventually allowed", true: "bounded wait"}[timeout], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				backend := &mockBackend{}
				project := Project{Target: testTarget}
				backend.On("Resolve", mock.Anything, testTarget).Return(project, nil).Once()
				backend.On("EnsureGrant", mock.Anything, project).Return(true, nil).Once()
				service, err := NewService(backend, &Options{PropagationTimeout: 4 * time.Second})
				require.NoError(t, err)
				start := time.Now()
				calls := 0
				result, err := service.Ensure(t.Context(), Request{Target: testTarget, Operation: "skills.write",
					Policy: GrantFoundryUser, Probe: func(context.Context, Project) (State, error) {
						calls++
						if !timeout && calls == 4 {
							return Allowed, nil
						}
						return Denied, nil
					}})
				if timeout {
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.Equal(t, Denied, result.State)
					require.Equal(t, 4*time.Second, time.Since(start))
				} else {
					require.NoError(t, err)
					require.Equal(t, Allowed, result.State)
					require.Equal(t, 3*time.Second, time.Since(start))
				}
				require.True(t, result.AssignmentCreated)
				backend.AssertExpectations(t)
			})
		})
	}
}

func TestEnsureValidation(t *testing.T) {
	probe := func(context.Context, Project) (State, error) { t.Fatal("probe must not run"); return Unknown, nil }
	for _, tt := range []struct {
		name    string
		target  Target
		policy  GrantPolicy
		probe   Probe
		cancel  bool
		wantErr string
	}{
		{"missing probe", testTarget, CheckOnly, nil, false, "non-mutating"},
		{"invalid policy", testTarget, "auto", probe, false, "policy"},
		{"canceled", testTarget, CheckOnly, probe, true, "canceled"},
		{"missing resource ID", Target{Endpoint: testTarget.Endpoint}, CheckOnly, probe, false, "ARM resource ID"},
		{"account scope", Target{ProjectID: strings.TrimSuffix(testProjectID, "/projects/project"), Endpoint: testTarget.Endpoint},
			CheckOnly, probe, false, "ARM resource ID"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backend := &mockBackend{}
			service, err := NewService(backend, nil)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			result, err := service.Ensure(ctx, Request{Target: tt.target, Operation: "toolboxes.write",
				Policy: tt.policy, Probe: tt.probe})
			require.ErrorContains(t, err, tt.wantErr)
			require.Equal(t, Unknown, result.State)
			backend.AssertNotCalled(t, "Resolve", mock.Anything, mock.Anything)
		})
	}
	_, err := NewService(nil, nil)
	require.Error(t, err)
	_, err = NewService(&mockBackend{}, &Options{PropagationTimeout: -time.Second})
	require.Error(t, err)
	service, err := NewService(&mockBackend{}, &Options{InitialDelay: 3 * time.Second})
	require.NoError(t, err)
	require.Equal(t, 3*time.Second, service.options.InitialDelay)
}

func TestEnsureResolutionFailure(t *testing.T) {
	backend := &mockBackend{}
	failure := errors.New("ARM project cannot be verified")
	backend.On("Resolve", mock.Anything, testTarget).Return(Project{}, failure).Once()
	service, err := NewService(backend, nil)
	require.NoError(t, err)
	result, err := service.Ensure(t.Context(), Request{Target: testTarget, Operation: "toolboxes.write",
		Policy: GrantFoundryUser, Probe: func(context.Context, Project) (State, error) {
			t.Fatal("an unverified target must not be probed")
			return Denied, nil
		}})
	require.ErrorIs(t, err, failure)
	require.Equal(t, Unknown, result.State)
	backend.AssertNotCalled(t, "EnsureGrant", mock.Anything, mock.Anything)
}

func TestEnsureCancellationAndProbeErrors(t *testing.T) {
	for _, stage := range []string{"resolve", "probe", "grant", "propagation probe"} {
		t.Run(stage, func(t *testing.T) {
			backend := &mockBackend{}
			project := Project{Target: testTarget}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backend.On("Resolve", mock.Anything, testTarget).Run(func(mock.Arguments) {
				if stage == "resolve" {
					cancel()
				}
			}).Return(project, nil).Once()
			if stage == "grant" || stage == "propagation probe" {
				backend.On("EnsureGrant", mock.Anything, project).Run(func(mock.Arguments) {
					if stage == "grant" {
						cancel()
					}
				}).Return(true, nil).Once()
			}
			service, err := NewService(backend, nil)
			require.NoError(t, err)
			probeFailure := errors.New("probe connection lost")
			calls := 0
			result, err := service.Ensure(ctx, Request{Target: testTarget, Operation: "toolboxes.write",
				Policy: GrantFoundryUser, Probe: func(context.Context, Project) (State, error) {
					calls++
					if stage == "probe" {
						cancel()
					}
					if calls > 1 {
						return Denied, probeFailure
					}
					return Denied, nil
				}})
			if stage == "propagation probe" {
				require.ErrorIs(t, err, probeFailure)
				require.Equal(t, Unknown, result.State)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
			if stage == "grant" || stage == "propagation probe" {
				require.True(t, result.AssignmentCreated, "preserve partial grant outcome")
			} else {
				backend.AssertNotCalled(t, "EnsureGrant", mock.Anything, mock.Anything)
			}
			backend.AssertExpectations(t)
		})
	}
}

func TestEnsureConcurrentChecksAreNotCached(t *testing.T) {
	backend := &mockBackend{}
	project := Project{Target: testTarget}
	backend.On("Resolve", mock.Anything, testTarget).Return(project, nil).Times(20)
	service, err := NewService(backend, nil)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			result, err := service.Ensure(t.Context(), Request{Target: testTarget, Operation: "datasets.read",
				Probe: func(context.Context, Project) (State, error) { return Allowed, nil }})
			if err != nil || result.State != Allowed {
				t.Errorf("unexpected access result: %v, %v", result.State, err)
			}
		})
	}
	wg.Wait()
	backend.AssertExpectations(t)
}
