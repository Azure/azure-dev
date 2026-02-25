// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type testCachedClient struct {
	id int
}

func TestClientCacheGetOrCreate_CacheMissCreatesClient(t *testing.T) {
	cache := clientCache[*testCachedClient]{}
	callCount := 0

	client, err := cache.GetOrCreate("sub", func() (*testCachedClient, error) {
		callCount++
		return &testCachedClient{id: 1}, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if client == nil || client.id != 1 {
		t.Fatalf("expected client id 1, got %#v", client)
	}

	if callCount != 1 {
		t.Fatalf("expected factory to be called once, got %d", callCount)
	}
}

func TestClientCacheGetOrCreate_CacheHitReturnsSameClient(t *testing.T) {
	cache := clientCache[*testCachedClient]{}
	callCount := 0

	first, err := cache.GetOrCreate("sub", func() (*testCachedClient, error) {
		callCount++
		return &testCachedClient{id: 1}, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	second, err := cache.GetOrCreate("sub", func() (*testCachedClient, error) {
		callCount++
		return &testCachedClient{id: 2}, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if first != second {
		t.Fatal("expected cache hit to return same client instance")
	}

	if callCount != 1 {
		t.Fatalf("expected factory to be called once, got %d", callCount)
	}
}

func TestClientCacheGetOrCreate_DifferentKeysGetDifferentClients(t *testing.T) {
	cache := clientCache[*testCachedClient]{}

	first, err := cache.GetOrCreate("sub1", func() (*testCachedClient, error) {
		return &testCachedClient{id: 1}, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	second, err := cache.GetOrCreate("sub2", func() (*testCachedClient, error) {
		return &testCachedClient{id: 2}, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if first == second {
		t.Fatal("expected different keys to return different client instances")
	}
}

func TestClientCacheGetOrCreate_ConcurrentAccessSafe(t *testing.T) {
	cache := clientCache[*testCachedClient]{}
	const goroutines = 64
	results := make([]*testCachedClient, goroutines)
	var calls atomic.Int32
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for i := range goroutines {
		go func(index int) {
			defer wg.Done()
			client, err := cache.GetOrCreate("sub", func() (*testCachedClient, error) {
				id := int(calls.Add(1))
				return &testCachedClient{id: id}, nil
			})
			if err != nil {
				t.Errorf("expected no error, got %v", err)
				return
			}

			results[index] = client
		}(i)
	}

	wg.Wait()

	if results[0] == nil {
		t.Fatal("expected first result to be non-nil")
	}

	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatalf("expected all goroutines to receive same client instance at index %d", i)
		}
	}
}

func TestClientCacheGetOrCreate_FactoryErrorPropagates(t *testing.T) {
	cache := clientCache[*testCachedClient]{}
	wantErr := errors.New("boom")

	client, err := cache.GetOrCreate("sub", func() (*testCachedClient, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}

	if client != nil {
		t.Fatalf("expected nil client on error, got %#v", client)
	}
}
