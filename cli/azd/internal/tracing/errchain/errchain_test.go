// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errchain

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

// selfRefErr unwraps to itself to exercise the cycle cap.
type selfRefErr struct{}

func (s *selfRefErr) Error() string { return "self-ref" }
func (s *selfRefErr) Unwrap() error { return s }

func TestTypes_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, Types(nil))
}

func TestTypes_BareError(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"*errors.errorString"}, Types(errors.New("boom")))
}

func TestTypes_FmtErrorfWraps(t *testing.T) {
	t.Parallel()
	inner := &typedErr{msg: "typed"}
	err := fmt.Errorf("ctx: %w", inner)
	require.Equal(t, []string{"*fmt.wrapError", "*errchain.typedErr"}, Types(err))
}

func TestTypes_DeeplyWrapped(t *testing.T) {
	t.Parallel()
	leaf := &typedErr{msg: "leaf"}
	mid := &wrappedErr{msg: "mid", inner: leaf}
	outer := fmt.Errorf("outer: %w", mid)
	require.Equal(
		t,
		[]string{"*fmt.wrapError", "*errchain.wrappedErr", "*errchain.typedErr"},
		Types(outer),
	)
}

func TestTypes_Joined(t *testing.T) {
	t.Parallel()
	a := &typedErr{msg: "a"}
	b := &typedErr{msg: "b"}
	joined := errors.Join(a, b)
	require.Equal(
		t,
		// joinError walks depth-first across slice order
		[]string{"*errors.joinError", "*errchain.typedErr", "*errchain.typedErr"},
		Types(joined),
	)
}

func TestTypes_FmtMultipleWraps(t *testing.T) {
	t.Parallel()
	a := &typedErr{msg: "a"}
	b := &typedErr{msg: "b"}
	err := fmt.Errorf("two: %w / %w", a, b)
	require.Equal(
		t,
		// fmt.Errorf with multiple %w produces *fmt.wrapErrors
		[]string{"*fmt.wrapErrors", "*errchain.typedErr", "*errchain.typedErr"},
		Types(err),
	)
}

func TestTypes_NilChildSkipped(t *testing.T) {
	t.Parallel()
	a := &typedErr{msg: "a"}
	joined := errors.Join(nil, a, nil)
	require.Equal(
		t,
		[]string{"*errors.joinError", "*errchain.typedErr"},
		Types(joined),
	)
}

func TestTypes_RespectsCap(t *testing.T) {
	t.Parallel()
	// Build a chain longer than MaxChainLen.
	var err error = &typedErr{msg: "leaf"}
	for range 100 {
		err = fmt.Errorf("wrap: %w", err)
	}
	got := Types(err)
	require.Len(t, got, MaxChainLen)
}

func TestTypes_CycleSafe(t *testing.T) {
	t.Parallel()
	// selfRefErr.Unwrap() returns itself; only the cap stops us.
	got := Types(&selfRefErr{})
	require.Len(t, got, MaxChainLen)
	for _, n := range got {
		require.Equal(t, "*errchain.selfRefErr", n)
	}
}

func TestDeepestNamedType_Nil(t *testing.T) {
	t.Parallel()
	require.Equal(t, "<nil>", DeepestNamedType(nil))
}

func TestDeepestNamedType_BareError(t *testing.T) {
	t.Parallel()
	// No named non-generic type → fall back to leaf.
	require.Equal(t, "*errors.errorString", DeepestNamedType(errors.New("boom")))
}

func TestDeepestNamedType_TypedLeaf(t *testing.T) {
	t.Parallel()
	require.Equal(t, "*errchain.typedErr", DeepestNamedType(&typedErr{msg: "x"}))
}

func TestDeepestNamedType_FmtWrapsTyped(t *testing.T) {
	t.Parallel()
	inner := &typedErr{msg: "x"}
	err := fmt.Errorf("ctx: %w", inner)
	require.Equal(t, "*errchain.typedErr", DeepestNamedType(err))
}

func TestDeepestNamedType_NestedTypedSurvives(t *testing.T) {
	t.Parallel()
	leaf := &typedErr{msg: "x"}
	mid := &wrappedErr{msg: "mid", inner: leaf}
	err := fmt.Errorf("outer: %w", mid)
	// Both wrappedErr and typedErr are non-generic; deepest wins.
	require.Equal(t, "*errchain.typedErr", DeepestNamedType(err))
}

func TestDeepestNamedType_JoinedFirstChild(t *testing.T) {
	t.Parallel()
	a := &typedErr{msg: "a"}
	b := &wrappedErr{msg: "b"}
	joined := errors.Join(a, b)
	// Joined: deepest-named follows the first non-nil child.
	require.Equal(t, "*errchain.typedErr", DeepestNamedType(joined))
}

func TestDeepestNamedType_AllGeneric(t *testing.T) {
	t.Parallel()
	// All wrappers are generic; we expect the leaf type.
	err := fmt.Errorf("ctx: %w", errors.New("boom"))
	require.Equal(t, "*errors.errorString", DeepestNamedType(err))
}

func TestIsGenericWrapper(t *testing.T) {
	t.Parallel()
	require.True(t, IsGenericWrapper("*errors.errorString"))
	require.True(t, IsGenericWrapper("*fmt.wrapError"))
	require.True(t, IsGenericWrapper("*fmt.wrapErrors"))
	require.True(t, IsGenericWrapper("*errors.joinError"))
	require.True(t, IsGenericWrapper("*errorhandler.ErrorWithSuggestion"))
	require.True(t, IsGenericWrapper("*internal.ErrorWithTraceId"))
	require.False(t, IsGenericWrapper("*azcore.ResponseError"))
	require.False(t, IsGenericWrapper("*errchain.typedErr"))
}
