package lazy

import (
	"errors"
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
	callCount := 0

	initFn := func() (string, error) {
		callCount++
		time.Sleep(time.Millisecond * 200)
		return expected, nil
	}

	instance := NewLazy(initFn)

	var actual string
	var err error

	res := make(chan bool, 2)

	go func() {
		actual, err = instance.GetValue()
		res <- true
	}()

	go func() {
		actual, err = instance.GetValue()
		res <- true
	}()

	done := <-res

	require.True(t, done)
	require.Equal(t, expected, actual)
	require.NoError(t, err)
	require.Equal(t, 1, callCount)
}
