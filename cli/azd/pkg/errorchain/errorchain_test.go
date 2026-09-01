// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorchain

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type typedErr struct{ msg string }

func (t *typedErr) Error() string { return t.msg }

type wrappedErr struct {
	msg   string
	inner error
}

func (w *wrappedErr) Error() string { return w.msg }
func (w *wrappedErr) Unwrap() error { return w.inner }

type selfRefErr struct{}

func (s *selfRefErr) Error() string { return "self-ref" }
func (s *selfRefErr) Unwrap() error { return s }

func TestTypes(t *testing.T) {
	t.Parallel()

	leaf := &typedErr{msg: "leaf"}
	err := fmt.Errorf("outer: %w", &wrappedErr{msg: "mid", inner: leaf})

	require.Equal(t, []string{"*fmt.wrapError", "*errorchain.wrappedErr", "*errorchain.typedErr"}, Types(err))
}

func TestTypesJoined(t *testing.T) {
	t.Parallel()

	a := &typedErr{msg: "a"}
	b := &typedErr{msg: "b"}
	require.Equal(
		t,
		[]string{"*errors.joinError", "*errorchain.typedErr", "*errorchain.typedErr"},
		Types(errors.Join(a, b)),
	)
}

func TestCauseTypesFiltersAndDeduplicates(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("outer: %w", &wrappedErr{
		msg:   "mid",
		inner: &typedErr{msg: "leaf"},
	})

	require.Equal(t, []string{"*errorchain.wrappedErr", "*errorchain.typedErr"}, CauseTypes(err))
	require.Equal(t, []string{"*errorchain.typedErr"}, CauseTypes(errors.Join(&typedErr{}, &typedErr{})))
	require.Nil(t, CauseTypes(errors.New("plain")))
}

func TestCauseTypesCycleSafe(t *testing.T) {
	t.Parallel()

	err := &selfRefErr{}
	require.Equal(t, []string{"*errorchain.selfRefErr"}, Types(err))
	require.Equal(t, "*errorchain.selfRefErr", DeepestNamedType(err))
	require.Equal(t, []string{"*errorchain.selfRefErr"}, CauseTypes(err))
}

func TestNormalizeCauseTypesRejectsUnsafeValues(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		[]string{"*azcore.ResponseError", "pkg.TypedError"},
		NormalizeCauseTypes([]string{
			"*azcore.ResponseError",
			"*azcore.ResponseError",
			"message with spaces",
			"pkg.TypedError",
		}),
	)
}
