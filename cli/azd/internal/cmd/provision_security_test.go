// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

// mockEnvManager implements environment.Manager for testing syncEnvManager
// serialization.
type mockEnvManager struct {
	environment.Manager
	saveCalls   atomic.Int32
	maxConcur   atomic.Int32
	curConcur   atomic.Int32
	saveFunc    func(ctx context.Context, env *environment.Environment) error
	saveOptFunc func(
		ctx context.Context,
		env *environment.Environment,
		opts *environment.SaveOptions,
	) error
}

func (m *mockEnvManager) Save(
	ctx context.Context, env *environment.Environment,
) error {
	cur := m.curConcur.Add(1)
	defer m.curConcur.Add(-1)

	// Track maximum concurrent callers
	for {
		old := m.maxConcur.Load()
		if cur <= old || m.maxConcur.CompareAndSwap(old, cur) {
			break
		}
	}

	m.saveCalls.Add(1)

	if m.saveFunc != nil {
		return m.saveFunc(ctx, env)
	}
	return nil
}

func (m *mockEnvManager) SaveWithOptions(
	ctx context.Context,
	env *environment.Environment,
	opts *environment.SaveOptions,
) error {
	cur := m.curConcur.Add(1)
	defer m.curConcur.Add(-1)

	for {
		old := m.maxConcur.Load()
		if cur <= old || m.maxConcur.CompareAndSwap(old, cur) {
			break
		}
	}

	m.saveCalls.Add(1)

	if m.saveOptFunc != nil {
		return m.saveOptFunc(ctx, env, opts)
	}
	return nil
}

// syncEnvManager wraps an [environment.Manager] with a mutex to serialize
// Save and SaveWithOptions calls. This prevents data races when multiple
// goroutines (e.g., parallel provisioning layers) write environment state
// concurrently.
type syncEnvManager struct {
	environment.Manager
	mu sync.Mutex
}

func (s *syncEnvManager) Save(
	ctx context.Context, env *environment.Environment,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Manager.Save(ctx, env)
}

func (s *syncEnvManager) SaveWithOptions(
	ctx context.Context,
	env *environment.Environment,
	options *environment.SaveOptions,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Manager.SaveWithOptions(ctx, env, options)
}

func TestProvisionSecurity_syncEnvManager_serializesSaves(
	t *testing.T,
) {
	t.Run(
		"concurrent Save calls are serialized by mutex",
		func(t *testing.T) {
			mock := &mockEnvManager{}

			// Add a small busy-wait in the mock to increase the
			// chance of detecting unserialized access.
			mock.saveFunc = func(
				_ context.Context, _ *environment.Environment,
			) error {
				sum := 0
				for i := range 10000 {
					sum += i
				}
				_ = sum
				return nil
			}

			safe := &syncEnvManager{Manager: mock}
			env := environment.NewWithValues(
				"test-env", map[string]string{"KEY": "VALUE"},
			)

			const goroutines = 50
			var wg sync.WaitGroup

			for range goroutines {
				wg.Go(func() {
					_ = safe.Save(t.Context(), env)
				})
			}
			wg.Wait()

			require.Equal(t,
				int32(goroutines), mock.saveCalls.Load(),
				"all Save calls should complete",
			)

			// syncEnvManager's mutex should serialize access,
			// so maxConcur should be 1
			require.Equal(t,
				int32(1), mock.maxConcur.Load(),
				"syncEnvManager should serialize concurrent "+
					"Save calls (max concurrency should be 1)",
			)
		})

	t.Run(
		"concurrent SaveWithOptions calls are serialized",
		func(t *testing.T) {
			mock := &mockEnvManager{}
			mock.saveOptFunc = func(
				_ context.Context,
				_ *environment.Environment,
				_ *environment.SaveOptions,
			) error {
				sum := 0
				for i := range 10000 {
					sum += i
				}
				_ = sum
				return nil
			}

			safe := &syncEnvManager{Manager: mock}
			env := environment.NewWithValues(
				"test-env", map[string]string{"KEY": "VALUE"},
			)

			const goroutines = 50
			var wg sync.WaitGroup

			for range goroutines {
				wg.Go(func() {
					_ = safe.SaveWithOptions(
						t.Context(), env, nil,
					)
				})
			}
			wg.Wait()

			require.Equal(t,
				int32(goroutines), mock.saveCalls.Load(),
			)
			require.Equal(t,
				int32(1), mock.maxConcur.Load(),
				"syncEnvManager should serialize concurrent "+
					"SaveWithOptions calls",
			)
		})

	t.Run(
		"mixed Save and SaveWithOptions are serialized",
		func(t *testing.T) {
			mock := &mockEnvManager{}
			mock.saveFunc = func(
				_ context.Context, _ *environment.Environment,
			) error {
				sum := 0
				for i := range 5000 {
					sum += i
				}
				_ = sum
				return nil
			}
			mock.saveOptFunc = func(
				_ context.Context,
				_ *environment.Environment,
				_ *environment.SaveOptions,
			) error {
				sum := 0
				for i := range 5000 {
					sum += i
				}
				_ = sum
				return nil
			}

			safe := &syncEnvManager{Manager: mock}
			env := environment.NewWithValues(
				"test-env", map[string]string{"KEY": "VALUE"},
			)

			const goroutines = 30
			var wg sync.WaitGroup

			for range goroutines {
				wg.Go(func() {
					_ = safe.Save(t.Context(), env)
				})
				wg.Go(func() {
					_ = safe.SaveWithOptions(
						t.Context(), env, nil,
					)
				})
			}
			wg.Wait()

			require.Equal(t,
				int32(goroutines*2), mock.saveCalls.Load(),
			)
			require.Equal(t,
				int32(1), mock.maxConcur.Load(),
				"mixed Save/SaveWithOptions should be "+
					"serialized by the same mutex",
			)
		})
}

func TestProvisionSecurity_environmentClone_isIndependent(
	t *testing.T,
) {
	t.Run(
		"cloned environment does not share map with original",
		func(t *testing.T) {
			original := environment.NewWithValues(
				"original", map[string]string{
					"SHARED_KEY":   "original-value",
					"AZURE_REGION": "eastus",
				},
			)

			// This is the exact clone pattern used in
			// provisionLayersGraph:
			// layerEnv := environment.NewWithValues(
			//     p.env.Name(), p.env.Dotenv(),
			// )
			cloned := environment.NewWithValues(
				original.Name(), original.Dotenv(),
			)

			// Mutating the clone should NOT affect the original
			cloned.DotenvSet("SHARED_KEY", "cloned-value")
			cloned.DotenvSet("NEW_KEY", "new-value")

			require.Equal(t,
				"original-value",
				original.Dotenv()["SHARED_KEY"],
				"mutating clone must not affect original "+
					"environment",
			)
			_, hasNew := original.Dotenv()["NEW_KEY"]
			require.False(t, hasNew,
				"keys added to clone must not appear in "+
					"original environment",
			)
		})

	t.Run(
		"multiple clones are independent of each other",
		func(t *testing.T) {
			source := environment.NewWithValues(
				"source", map[string]string{"BASE": "value"},
			)

			clone1 := environment.NewWithValues(
				source.Name(), source.Dotenv(),
			)
			clone2 := environment.NewWithValues(
				source.Name(), source.Dotenv(),
			)

			clone1.DotenvSet("LAYER", "layer1")
			clone2.DotenvSet("LAYER", "layer2")

			require.Equal(t, "layer1", clone1.Dotenv()["LAYER"])
			require.Equal(t, "layer2", clone2.Dotenv()["LAYER"])

			_, hasLayer := source.Dotenv()["LAYER"]
			require.False(t, hasLayer,
				"source must not have LAYER key set by clones",
			)
		})

	t.Run(
		"concurrent writes to independent clones do not race",
		func(t *testing.T) {
			source := environment.NewWithValues(
				"source", map[string]string{"INITIAL": "value"},
			)

			const cloneCount = 10
			clones := make([]*environment.Environment, cloneCount)
			for i := range clones {
				clones[i] = environment.NewWithValues(
					source.Name(), source.Dotenv(),
				)
			}

			var wg sync.WaitGroup

			for i := range cloneCount {
				wg.Go(func() {
					env := clones[i]
					for range 100 {
						env.DotenvSet("KEY", "value")
					}
				})
			}
			wg.Wait()

			// Source must remain untouched
			require.Equal(t,
				"value", source.Dotenv()["INITIAL"],
			)
			_, hasKey := source.Dotenv()["KEY"]
			require.False(t, hasKey)
		})
}
