// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"fmt"
	"reflect"
	"time"
)

// Clone returns a detached configuration, including any unsaved secrets in its
// in-memory vault. A nil configuration becomes an empty configuration.
// Config implementations wrapping another configuration can implement
// Clone() (Config, error) to preserve state not exposed by Raw.
// The caller must synchronize access to configurations that are not thread-safe.
func Clone(c Config) (Config, error) {
	if c == nil {
		return NewEmptyConfig(), nil
	}
	if cloner, ok := c.(interface{ Clone() (Config, error) }); ok {
		return cloner.Clone()
	}
	data, err := CloneValue(c.Raw())
	if err != nil {
		return nil, err
	}
	if _, hasVault := data[vaultKeyName]; hasVault {
		return nil, fmt.Errorf("configuration type %T must implement Clone to preserve vault state", c)
	}
	return NewConfig(data), nil
}

func (c *config) Clone() (Config, error) {
	data, err := CloneValue(c.data)
	if err != nil {
		return nil, err
	}
	cloned := &config{
		data:    data,
		vaultId: c.vaultId,
	}
	if c.vault != nil {
		cloned.vault, err = Clone(c.vault)
		if err != nil {
			return nil, fmt.Errorf("cloning configuration vault: %w", err)
		}
	}
	return cloned, nil
}

// CloneValue copies a configuration value without converting its types through
// JSON. Maps, slices, arrays, pointers, and exported struct fields are copied
// recursively. It rejects cycles, unsupported mutable values (such as channels),
// mutable map keys, and unexported mutable struct fields instead of sharing them.
// Repeated references are cloned independently; alias relationships are not
// preserved. time.Time is treated as an immutable value.
func CloneValue[T any](value T) (T, error) {
	cloned, err := cloneValue(reflect.ValueOf(value), map[cloneVisit]bool{})
	if err != nil {
		var zero T
		return zero, err
	}
	if !cloned.IsValid() {
		return value, nil
	}
	return cloned.Interface().(T), nil
}

type cloneVisit struct {
	typ    reflect.Type
	ptr    uintptr
	length int // Shorter subslices can share a start address without forming a cycle.
}

func cloneValue(value reflect.Value, active map[cloneVisit]bool) (reflect.Value, error) {
	// Only track the current path. Two fields can share a value without forming a cycle.
	switch value.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		visit := cloneVisit{typ: value.Type(), ptr: value.Pointer()}
		if value.Kind() == reflect.Slice {
			visit.length = value.Len()
		}
		if active[visit] {
			return reflect.Value{}, fmt.Errorf("cannot clone cyclic configuration value of type %s", value.Type())
		}
		active[visit] = true
		defer delete(active, visit)
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		element, err := cloneValue(value.Elem(), active)
		if err != nil {
			return reflect.Value{}, err
		}
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(element)
		return cloned, nil
	case reflect.Map:
		if !immutableType(value.Type().Key()) {
			return reflect.Value{}, fmt.Errorf("cannot clone mutable configuration map keys of type %s", value.Type().Key())
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		for iter := value.MapRange(); iter.Next(); {
			element, err := cloneValue(iter.Value(), active)
			if err != nil {
				return reflect.Value{}, err
			}
			cloned.SetMapIndex(iter.Key(), element)
		}
		return cloned, nil
	case reflect.Slice, reflect.Array:
		var cloned reflect.Value
		if value.Kind() == reflect.Slice {
			cloned = reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		} else {
			cloned = reflect.New(value.Type()).Elem()
		}
		for i := range value.Len() {
			element, err := cloneValue(value.Index(i), active)
			if err != nil {
				return reflect.Value{}, err
			}
			cloned.Index(i).Set(element)
		}
		return cloned, nil
	case reflect.Pointer:
		element, err := cloneValue(value.Elem(), active)
		if err != nil {
			return reflect.Value{}, err
		}
		cloned := reflect.New(value.Type().Elem())
		cloned.Elem().Set(element)
		return cloned.Convert(value.Type()), nil
	case reflect.Struct:
		if value.Type() == reflect.TypeFor[time.Time]() {
			return value, nil
		}
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(value)
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if !field.IsExported() {
				if !immutableType(field.Type) {
					return reflect.Value{}, fmt.Errorf(
						"cannot clone unexported mutable configuration field %s.%s", value.Type(), field.Name)
				}
				continue
			}
			element, err := cloneValue(value.Field(i), active)
			if err != nil {
				return reflect.Value{}, err
			}
			cloned.Field(i).Set(element)
		}
		return cloned, nil
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return reflect.Value{}, fmt.Errorf("cannot clone unsupported configuration value of type %s", value.Type())
	default:
		return value, nil
	}
}

func immutableType(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return false
	case reflect.Array:
		return immutableType(typ.Elem())
	case reflect.Struct:
		if typ == reflect.TypeFor[time.Time]() {
			return true
		}
		for i := range typ.NumField() {
			if !immutableType(typ.Field(i).Type) {
				return false
			}
		}
	}
	return true
}
