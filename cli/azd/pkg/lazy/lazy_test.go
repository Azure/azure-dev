// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package lazy

import (
	"errors"
	"sync"
	"testing"
	"time"

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

func Test_Lazy_Init_BypassInitializer(t *testing.T) {
	initFn := func() (string, error) {
		require.Fail(t, "THIS SHOULD NEVER BE CALLED")
		return "", errors.New("NEVER USED")
	}

	instance := NewLazy(initFn)
	instance.SetValue("explicitly set!")
	actualValue, err := instance.GetValue()
	require.Equal(t, "explicitly set!", actualValue)
	require.NoError(t, err)
}

func Test_Lazy_SetValueRecoversFromInitializationError(t *testing.T) {
	initializerCalls := 0
	instance := NewLazy(func() (string, error) {
		initializerCalls++
		return "", errors.New("FAIL ON PURPOSE")
	})

	value, err := instance.GetValue()
	require.EqualError(t, err, "FAIL ON PURPOSE")
	require.Empty(t, value)
	require.Equal(t, 1, initializerCalls)

	instance.SetValue("recovered")
	value, err = instance.GetValue()
	require.NoError(t, err)
	require.Equal(t, "recovered", value)
	require.Equal(t, 1, initializerCalls)
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

func Test_Lazy_GetValueAndSetValue_Concurrent(t *testing.T) {
	instance := From(0)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup

	for writerValue := 1; writerValue <= 4; writerValue++ {
		waitGroup.Go(func() {
			<-start
			for range 1_000 {
				instance.SetValue(writerValue)
			}
		})
		waitGroup.Go(func() {
			<-start
			for range 1_000 {
				_, _ = instance.GetValue()
			}
		})
	}

	close(start)
	waitGroup.Wait()
}

func Test_Lazy_GetValue_Concurrent(t *testing.T) {
	expected := "test"
	callCount := 0

	initFn := func() (string, error) {
		callCount++
		// justified: simulates a slow initializer so both goroutines are guaranteed to
		// reach GetValue() before init completes, verifying only one runs the initFn.
		time.Sleep(time.Millisecond * 200)
		return expected, nil
	}

	instance := NewLazy(initFn)

	type result struct {
		value string
		err   error
	}
	results := make(chan result, 2)

	for range 2 {
		go func() {
			actual, err := instance.GetValue()
			results <- result{value: actual, err: err}
		}()
	}

	for range 2 {
		actual := <-results
		require.Equal(t, expected, actual.value)
		require.NoError(t, actual.err)
	}
	require.Equal(t, 1, callCount)
}
