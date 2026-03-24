// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package syncmap

import (
	"sort"
	"sync"
	"testing"
)

func TestStore_and_Load(t *testing.T) {
	var m Map[string, int]

	m.Store("a", 1)
	m.Store("b", 2)

	v, ok := m.Load("a")
	if !ok || v != 1 {
		t.Fatalf("Load(a) = (%v, %v), want (1, true)", v, ok)
	}

	v, ok = m.Load("b")
	if !ok || v != 2 {
		t.Fatalf("Load(b) = (%v, %v), want (2, true)", v, ok)
	}
}

func TestLoad_missing_key(t *testing.T) {
	var m Map[string, int]

	v, ok := m.Load("missing")
	if ok {
		t.Fatal("Load(missing) should return false")
	}
	if v != 0 {
		t.Fatalf("Load(missing) zero-value = %v, want 0", v)
	}
}

func TestLoad_missing_key_pointer_type(t *testing.T) {
	var m Map[string, *int]

	v, ok := m.Load("missing")
	if ok {
		t.Fatal("Load(missing) should return false")
	}
	if v != nil {
		t.Fatalf("Load(missing) zero-value = %v, want nil", v)
	}
}

func TestStore_overwrites_existing(t *testing.T) {
	var m Map[string, string]

	m.Store("key", "old")
	m.Store("key", "new")

	v, ok := m.Load("key")
	if !ok || v != "new" {
		t.Fatalf("Load(key) = (%v, %v), want (new, true)", v, ok)
	}
}

func TestLoadOrStore_stores_new_value(t *testing.T) {
	var m Map[string, int]

	actual, loaded := m.LoadOrStore("k", 42)
	if loaded {
		t.Fatal("LoadOrStore should return loaded=false for new key")
	}
	if actual != 42 {
		t.Fatalf("LoadOrStore actual = %v, want 42", actual)
	}

	// Verify it persisted
	v, ok := m.Load("k")
	if !ok || v != 42 {
		t.Fatalf("Load(k) after LoadOrStore = (%v, %v), want (42, true)", v, ok)
	}
}

func TestLoadOrStore_returns_existing(t *testing.T) {
	var m Map[string, int]

	m.Store("k", 10)

	actual, loaded := m.LoadOrStore("k", 99)
	if !loaded {
		t.Fatal("LoadOrStore should return loaded=true for existing key")
	}
	if actual != 10 {
		t.Fatalf("LoadOrStore actual = %v, want 10 (existing)", actual)
	}
}

func TestLoadAndDelete_existing_key(t *testing.T) {
	var m Map[string, string]

	m.Store("x", "hello")

	v, loaded := m.LoadAndDelete("x")
	if !loaded {
		t.Fatal("LoadAndDelete should return loaded=true for existing key")
	}
	if v != "hello" {
		t.Fatalf("LoadAndDelete value = %v, want hello", v)
	}

	// Verify deletion
	_, ok := m.Load("x")
	if ok {
		t.Fatal("Load(x) should return false after LoadAndDelete")
	}
}

func TestLoadAndDelete_missing_key(t *testing.T) {
	var m Map[string, int]

	v, loaded := m.LoadAndDelete("nope")
	if loaded {
		t.Fatal("LoadAndDelete should return loaded=false for missing key")
	}
	if v != 0 {
		t.Fatalf("LoadAndDelete zero-value = %v, want 0", v)
	}
}

func TestDelete(t *testing.T) {
	var m Map[int, string]

	m.Store(1, "one")
	m.Delete(1)

	_, ok := m.Load(1)
	if ok {
		t.Fatal("Load(1) should return false after Delete")
	}
}

func TestDelete_missing_key_no_panic(t *testing.T) {
	var m Map[string, string]

	// Should not panic
	m.Delete("does-not-exist")
}

func TestRange(t *testing.T) {
	var m Map[string, int]

	m.Store("a", 1)
	m.Store("b", 2)
	m.Store("c", 3)

	var keys []string
	sum := 0
	m.Range(func(key string, value int) bool {
		keys = append(keys, key)
		sum += value
		return true
	})

	sort.Strings(keys)
	if len(keys) != 3 {
		t.Fatalf("Range visited %d keys, want 3", len(keys))
	}
	if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
		t.Fatalf("Range keys = %v, want [a b c]", keys)
	}
	if sum != 6 {
		t.Fatalf("Range sum = %d, want 6", sum)
	}
}

func TestRange_early_stop(t *testing.T) {
	var m Map[int, int]

	m.Store(1, 10)
	m.Store(2, 20)
	m.Store(3, 30)

	count := 0
	m.Range(func(key int, value int) bool {
		count++
		return false // stop after first
	})

	if count != 1 {
		t.Fatalf("Range with early stop visited %d keys, want 1", count)
	}
}

func TestRange_empty_map(t *testing.T) {
	var m Map[string, string]

	count := 0
	m.Range(func(key string, value string) bool {
		count++
		return true
	})

	if count != 0 {
		t.Fatalf("Range on empty map visited %d keys, want 0", count)
	}
}

func TestConcurrent_access(t *testing.T) {
	var m Map[int, int]
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m.Store(n, n*10)
		}(i)
	}
	wg.Wait()

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v, ok := m.Load(n)
			if !ok {
				t.Errorf("Load(%d) returned false", n)
				return
			}
			if v != n*10 {
				t.Errorf("Load(%d) = %d, want %d", n, v, n*10)
			}
		}(i)
	}
	wg.Wait()
}

func TestInteger_key_type(t *testing.T) {
	var m Map[int, string]

	m.Store(0, "zero")
	m.Store(-1, "neg")
	m.Store(42, "answer")

	v, ok := m.Load(0)
	if !ok || v != "zero" {
		t.Fatalf("Load(0) = (%v, %v), want (zero, true)", v, ok)
	}
	v, ok = m.Load(-1)
	if !ok || v != "neg" {
		t.Fatalf("Load(-1) = (%v, %v), want (neg, true)", v, ok)
	}
}

func TestStruct_value_type(t *testing.T) {
	type item struct {
		Name  string
		Count int
	}

	var m Map[string, item]

	m.Store("x", item{Name: "test", Count: 5})

	v, ok := m.Load("x")
	if !ok {
		t.Fatal("Load(x) returned false")
	}
	if v.Name != "test" || v.Count != 5 {
		t.Fatalf("Load(x) = %+v, want {Name:test Count:5}", v)
	}
}
