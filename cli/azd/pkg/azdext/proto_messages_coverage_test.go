// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// TestProtoMessages_ExerciseGenerated iterates over every proto message registered
// in the azdext package and exercises its generated methods (Reset, String,
// ProtoReflect, Descriptor, GetXxx getters) plus Marshal/Unmarshal round-trips.
// This is a broad, generic smoke test aimed at raising statement coverage of the
// generated *.pb.go files which otherwise contain hundreds of never-called
// getters and reflection helpers.
func TestProtoMessages_ExerciseGenerated(t *testing.T) {
	t.Parallel()

	var exercised int
	protoregistry.GlobalTypes.RangeMessages(func(mt protoreflect.MessageType) bool {
		name := string(mt.Descriptor().FullName())
		// Only exercise messages defined by this package (proto package = "azdext")
		// Map entry synthetic messages are registered as e.g. "azdext.Foo.BarEntry"
		// and are also fine to exercise since they live in the generated files.
		if !strings.HasPrefix(name, "azdext.") {
			return true
		}
		exerciseMessage(t, mt.New().Interface())
		exercised++
		return true
	})

	// Sanity check: the azdext package registers a large number of proto messages.
	// If this drops significantly, something is wrong with the registration.
	require.Greater(t, exercised, 100, "expected many azdext proto messages to be registered")
}

// exerciseMessage invokes the generated methods on a proto message so that they
// contribute to statement coverage. It exercises both the populated (non-nil
// receiver) and nil-receiver paths of getters.
func exerciseMessage(t *testing.T, m proto.Message) {
	t.Helper()
	name := string(m.ProtoReflect().Descriptor().FullName())

	t.Run(name, func(t *testing.T) {
		require.NotNil(t, m.ProtoReflect())

		// Marshal a zero-value message, then unmarshal into a fresh instance.
		// This exercises the reflection-backed Marshal/Unmarshal paths.
		data, err := proto.Marshal(m)
		require.NoError(t, err)

		fresh := m.ProtoReflect().New().Interface()
		require.NoError(t, proto.Unmarshal(data, fresh))

		// Call the generated String() and Reset() methods via reflection so
		// we don't depend on them being part of the proto.Message interface
		// in this version of the protobuf library.
		mv := reflect.ValueOf(m)
		if s := mv.MethodByName("String"); s.IsValid() {
			s.Call(nil)
		}
		if r := mv.MethodByName("Reset"); r.IsValid() {
			r.Call(nil)
		}

		// Call all zero-arg methods via reflection to pick up:
		//   - GetXxx() getters
		//   - Descriptor()
		// Do it against both a non-nil instance and an explicit typed-nil
		// instance so both branches of generated "if x != nil" checks run.
		ptrType := reflect.TypeOf(m)
		nilPtr := reflect.New(ptrType).Elem() // typed nil pointer

		// Non-nil receiver: getters must NOT panic on a valid instance. Any panic
		// here indicates a real regression in generated code and fails the test.
		callZeroArgMethods(t, mv, false /* allowPanic */)
		// Typed-nil receiver: generated getters typically guard with `if x != nil`,
		// but invoking via reflection on a typed-nil pointer can still panic for
		// methods that unconditionally deref (e.g. Descriptor). Allow (and swallow)
		// panics here; we're only exercising the `if x != nil` branches for coverage.
		callZeroArgMethods(t, nilPtr, true /* allowPanic */)
	})
}

func callZeroArgMethods(t *testing.T, v reflect.Value, allowPanic bool) {
	t.Helper()
	typ := v.Type()
	for i := 0; i < typ.NumMethod(); i++ {
		method := typ.Method(i)
		// Only zero-arg, exported methods.
		if method.Type.NumIn() != 1 { // receiver is arg 0
			continue
		}
		name := method.Name
		// Skip methods that we've already invoked (or whose invocation would
		// panic on typed nil via the reflect layer).
		switch name {
		case "Reset", "String", "ProtoReflect", "ProtoMessage":
			continue
		}
		// Only invoke Get* and Descriptor (the safe, side-effect-free generated helpers).
		if !strings.HasPrefix(name, "Get") && name != "Descriptor" {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil && !allowPanic {
					t.Errorf("unexpected panic invoking %s.%s on non-nil receiver: %v",
						typ.String(), name, r)
				}
			}()
			out := v.Method(i).Call(nil)
			_ = out
		}()
	}
}
