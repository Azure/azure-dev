// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package lazy

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Lazy_Init(t *testing.T) {
	expected := "test"
	ran := false

	initFn := func() (string, error) {
		ran = true
		return expected, nil
	}

	instance := NewLazy(initFn)
	require.NotNil(t, instance)
	require.False(t, ran)

	actual, err := instance.GetValue()
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.True(t, ran)
}

func Test_Lazy_GetValue(t *testing.T) {
	expected := "test"
	callCount := 0

	initFn := func() (string, error) {
		callCount++
		return expected, nil
	}

	instance := NewLazy(initFn)
	require.NotNil(t, instance)
	require.Equal(t, 0, callCount)

	// Make first call to GetValue()
	actual, err := instance.GetValue()
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.Equal(t, 1, callCount)

	// Make another call to GetValue
	actual2, err := instance.GetValue()
	require.NoError(t, err)
	require.Equal(t, expected, actual2)
	// Initializer should still only be called the first time
	require.Equal(t, 1, callCount)
}

func Test_Lazy_GetValue_With_Error(t *testing.T) {
	expected := "test"
	callCount := 0

	initFn := func() (string, error) {
		callCount++
		if callCount == 1 {
			return "", errors.New("error")
		}

		return expected, nil
	}

	instance := NewLazy(initFn)
	require.NotNil(t, instance)
	require.Equal(t, 0, callCount)

	// Make first call to GetValue()
	// Should return error
	actual, err := instance.GetValue()
	require.Error(t, err)
	require.Empty(t, actual)
	require.Equal(t, 1, callCount)

	// Make another call to GetValue
	// Subsequent request works
	actual2, err := instance.GetValue()
	require.NoError(t, err)
	require.Equal(t, expected, actual2)
	// Call count should now be 2
	require.Equal(t, 2, callCount)
}

func Test_Lazy_SetValue(t *testing.T) {
	instance := NewLazy(func() (string, error) {
		return "init", nil
	})

	actual, err := instance.GetValue()
	require.Equal(t, "init", actual)
	require.NoError(t, err)

	instance.SetValue("after")
	actual2, err := instance.GetValue()
	require.Equal(t, "after", actual2)
	require.NoError(t, err)
}

func Test_Lazy_GetValue_Concurrent(t *testing.T) {
	expected := "test"
	var callCount atomic.Int32
	initializerStarted := make(chan struct{})
	releaseInitializer := make(chan struct{})

	initFn := func() (string, error) {
		callCount.Add(1)
		close(initializerStarted)
		<-releaseInitializer
		return expected, nil
	}

	instance := NewLazy(initFn)

	type result struct {
		value string
		err   error
	}
	results := make(chan result, 2)
	gettersStarted := make(chan struct{}, 2)

	go func() {
		gettersStarted <- struct{}{}
		value, err := instance.GetValue()
		results <- result{value: value, err: err}
	}()
	<-initializerStarted

	go func() {
		gettersStarted <- struct{}{}
		value, err := instance.GetValue()
		results <- result{value: value, err: err}
	}()
	<-gettersStarted
	<-gettersStarted
	close(releaseInitializer)

	for range 2 {
		result := <-results
		require.Equal(t, expected, result.value)
		require.NoError(t, result.err)
	}
	require.Equal(t, int32(1), callCount.Load())
}

func Test_Lazy_InitializerCanSetValue(t *testing.T) {
	var instance *Lazy[string]
	instance = NewLazy(func() (string, error) {
		instance.SetValue("from setter")
		return "from initializer", nil
	})

	value, err := instance.GetValue()
	require.NoError(t, err)
	require.Equal(t, "from setter", value)

	value, err = instance.GetValue()
	require.NoError(t, err)
	require.Equal(t, "from setter", value)
}

func Test_Lazy_SetValueWinsOverRunningInitializer(t *testing.T) {
	tests := []struct {
		name           string
		initializerErr error
	}{
		{name: "initializer succeeds"},
		{name: "initializer fails", initializerErr: errors.New("initializer failed")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initializerStarted := make(chan struct{})
			releaseInitializer := make(chan struct{})
			instance := NewLazy(func() (string, error) {
				close(initializerStarted)
				<-releaseInitializer
				return "from initializer", test.initializerErr
			})

			type result struct {
				value string
				err   error
			}
			resultCh := make(chan result, 1)
			go func() {
				value, err := instance.GetValue()
				resultCh <- result{value: value, err: err}
			}()
			<-initializerStarted

			instance.SetValue("from setter")
			close(releaseInitializer)

			got := <-resultCh
			require.NoError(t, got.err)
			require.Equal(t, "from setter", got.value)

			value, err := instance.GetValue()
			require.NoError(t, err)
			require.Equal(t, "from setter", value)
		})
	}
}
