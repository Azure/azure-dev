// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package mapper provides a reflection-based type conversion framework.
//
// The mapper eliminates the need for explicit conversion functions between types
// by using runtime registration and automatic type resolution. This is particularly
// useful for converting between domain types and protocol buffer types without
// creating circular dependencies or requiring global awareness of conversion functions.
//
// Key Benefits:
//   - Eliminates circular dependencies between packages
//   - Single, consistent API: mapper.Convert(src, &dst)
//   - Context propagation for environment variable resolution
//   - Bidirectional conversions with automatic type matching
//   - Thread-safe registration and conversion
//
// Basic Usage:
//
//	// 1. Register converters (typically in init functions)
//	func init() {
//	    mapper.MustRegister(func(ctx context.Context, src *User) (*UserProto, error) {
//	        return &UserProto{Id: src.ID, Name: src.Name}, nil
//	    })
//	}
//
//	// 2. Perform conversions
//	var userProto *UserProto
//	err := mapper.Convert(user, &userProto)
//
// Context-Aware Conversions:
//
//	// Register converter that uses environment resolution
//	mapper.MustRegister(func(ctx context.Context, src *Config) (*ConfigProto, error) {
//	    resolver := mapper.GetResolver(ctx)
//	    expanded, err := src.Template.Envsubst(resolver)  // ${VAR} → actual value
//	    return &ConfigProto{Value: expanded}, err
//	})
//
//	// Convert with environment resolver
//	envResolver := func(key string) string { return os.Getenv(key) }
//	var configProto *ConfigProto
//	err := mapper.WithResolver(envResolver).Convert(config, &configProto)
//
// The Pointer Pattern:
//
// Conversions require passing the address of the destination variable:
//
//	var target *TargetType      // Initially nil
//	mapper.Convert(src, &target) // Pass address (&target)
//	// target now points to converted value
//
// This allows the mapper to modify what the pointer points to, changing it
// from nil to the actual converted object via reflection.
package mapper

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

// Resolver is a function that resolves environment variable names to their values.
//
// This function type is used by conversion functions to expand template strings
// containing variable references like ${REGISTRY_URL} or ${BUILD_VERSION}.
//
// Example implementations:
//
//	// Simple environment variable resolver
//	envResolver := func(key string) string {
//	    return os.Getenv(key)
//	}
//
//	// Custom resolver with defaults
//	customResolver := func(key string) string {
//	    if value := os.Getenv(key); value != "" {
//	        return value
//	    }
//	    // Return defaults for known keys
//	    switch key {
//	    case "REGISTRY":
//	        return "registry.azurecr.io"
//	    default:
//	        return ""
//	    }
//	}
type Resolver func(key string) string

// MapperFunc is the internal function signature for registered conversion functions.
//
// This type is used internally by the mapper framework after conversion functions
// are registered via Register or MustRegister. The framework wraps user-provided
// conversion functions in this signature to handle reflection and error propagation.
//
// Users typically don't interact with this type directly, but instead provide
// functions matching: func(context.Context, SourceType) (TargetType, error)
type MapperFunc func(ctx context.Context, src any, dst any) error

type resolverKeyType struct{}

var resolverKey = resolverKeyType{}

var (
	registry = make(map[[2]reflect.Type]MapperFunc)
	mu       sync.RWMutex
)

// Mapper provides a context-aware interface for type conversion.
//
// Mapper instances are created with specific contexts that can carry
// environment variable resolvers and other conversion-time data.
//
// The zero value is not useful; create instances using WithResolver():
//
//	mapper := mapper.WithResolver(envResolver)
//	err := mapper.Convert(src, &dst)
//
// Or use the package-level Convert function for simple conversions:
//
//	err := mapper.Convert(src, &dst)  // Uses background context
type Mapper struct {
	ctx context.Context
}

// Default mapper instance for convenience functions
var defaultMapper = &Mapper{ctx: context.Background()}

// Register a type converter function that transforms type S to type T.
//
// The mapper framework uses reflection to automatically route conversion requests
// to the appropriate registered function based on source and target types.
//
// Registration is type-safe at compile time and thread-safe at runtime.
// Each S→T type pair can only have one registered converter.
//
// Parameters:
//   - fn: Conversion function that takes (context, source) and returns (target, error)
//
// Returns an error if:
//   - fn is nil (ErrInvalidRegistration)
//   - A mapper is already registered for S→T (ErrDuplicateRegistration)
//
// Example:
//
//	// Register a converter from *User to *UserProto
//	err := mapper.Register(func(ctx context.Context, src *User) (*UserProto, error) {
//	    return &UserProto{
//	        Id:   src.ID,
//	        Name: src.Name,
//	    }, nil
//	})
//	if err != nil {
//	    // Handle registration error
//	}
//
// Use this when you need to handle registration errors programmatically.
// For init() functions where errors should halt startup, use MustRegister instead.
func Register[S, T any](fn func(context.Context, S) (T, error)) error {
	if fn == nil {
		return ErrInvalidRegistration
	}

	key := [2]reflect.Type{
		reflect.TypeOf((*S)(nil)).Elem(),
		reflect.TypeOf((*T)(nil)).Elem(),
	}

	mu.Lock()
	defer mu.Unlock()

	// Check for duplicate registration
	if _, exists := registry[key]; exists {
		return fmt.Errorf("%w: from %v to %v", ErrDuplicateRegistration, key[0], key[1])
	}

	registry[key] = func(ctx context.Context, src any, dst any) error {
		res, err := fn(ctx, src.(S))
		if err != nil {
			return err
		}
		reflect.ValueOf(dst).Elem().Set(reflect.ValueOf(res))
		return nil
	}
	return nil
}

// MustRegister a type converter function, panicking on any registration error.
//
// This is the preferred method for registering converters in init() functions
// where registration failures should halt application startup rather than
// being handled at runtime.
//
// Panics if:
//   - fn is nil (ErrInvalidRegistration)
//   - A mapper is already registered for S→T (ErrDuplicateRegistration)
//
// Example:
//
//	func init() {
//	    // Register bidirectional conversions
//	    mapper.MustRegister(func(ctx context.Context, src *Artifact) (*azdext.Artifact, error) {
//	        return &azdext.Artifact{
//	            Kind:     convertKind(src.Kind),
//	            Location: src.Location,
//	        }, nil
//	    })
//
//	    mapper.MustRegister(func(ctx context.Context, src *azdext.Artifact) (Artifact, error) {
//	        return Artifact{
//	            Kind:     convertKindFromProto(src.Kind),
//	            Location: src.Location,
//	        }, nil
//	    })
//	}
func MustRegister[S, T any](fn func(context.Context, S) (T, error)) {
	if err := Register(fn); err != nil {
		panic(fmt.Sprintf("mapper registration failed: %v", err))
	}
}

// Convert performs type conversion using the default mapper with background context.
//
// This is a convenience function for simple conversions that don't require
// context propagation (e.g., environment variable resolution).
//
// The destination must be a pointer to the target type. The mapper uses
// reflection to set the pointed-to value with the conversion result.
//
// Returns:
//   - NoMapperError if no converter is registered for src→dst types
//   - ConversionError if the converter function returns an error
//   - nil on successful conversion
//
// Example:
//
//	var target *UserProto
//	if err := mapper.Convert(user, &target); err != nil {
//	    var noMapper *NoMapperError
//	    if errors.As(err, &noMapper) {
//	        log.Printf("No converter from %v to %v", noMapper.SrcType, noMapper.DstType)
//	    }
//	    return err
//	}
//	// target now points to converted UserProto
//
// For conversions that need context (e.g., environment resolution), use:
//
//	mapper.WithResolver(envResolver).Convert(src, &dst)
func Convert(src any, dst any) error {
	return defaultMapper.Convert(src, dst)
}

// WithResolver returns a mapper configured with an environment variable resolver.
//
// The resolver enables conversion functions to expand environment variables
// and other template strings during the conversion process. This is essential
// for conversions involving configuration with ${VAR} placeholders.
//
// Parameters:
//   - resolver: Function that takes a variable name and returns its value.
//     If nil, returns a mapper with default background context.
//
// Example:
//
//	// Create resolver that reads from environment
//	envResolver := func(key string) string {
//	    return os.Getenv(key)
//	}
//
//	// Convert with environment resolution
//	var serviceConfig *azdext.ServiceConfig
//	err := mapper.WithResolver(envResolver).Convert(src, &serviceConfig)
//
//	// Inside conversion functions, retrieve resolver:
//	func convertService(ctx context.Context, src *Service) (*azdext.Service, error) {
//	    resolver := mapper.GetResolver(ctx)
//	    expandedImage := src.Image.Envsubst(resolver) // ${REGISTRY}/app:${TAG}
//	    return &azdext.Service{Image: expandedImage}, nil
//	}
func WithResolver(resolver Resolver) *Mapper {
	if resolver == nil {
		return &Mapper{ctx: context.Background()}
	}

	ctx := context.WithValue(context.Background(), resolverKey, func(key string) string {
		return resolver(key)
	})
	return &Mapper{ctx: ctx}
}

// Convert performs type conversion using this mapper's context.
//
// This method is called on mapper instances created with WithResolver() to
// perform conversions with context propagation (e.g., environment resolution).
//
// The conversion process:
//  1. Determines source and destination types via reflection
//  2. Looks up registered converter function for src→dst pair
//  3. Invokes converter with this mapper's context
//  4. Uses reflection to set the destination pointer
//
// The destination must be a pointer (&target). The mapper will modify
// what the pointer points to, changing it from nil to the converted value.
//
// Example conversion flow:
//
//	var proto *azdext.Artifact  // Initially nil
//	mapper.Convert(artifact, &proto)  // Pass address of pointer
//	// proto now points to converted azdext.Artifact
//
// Error handling:
//
//	if err := m.Convert(src, &dst); err != nil {
//	    if mapper.IsNoMapperError(err) {
//	        // No converter registered for this type pair
//	    } else if mapper.IsConversionError(err) {
//	        // Converter function returned an error
//	    }
//	}
func (m *Mapper) Convert(src any, dst any) error {
	srcType := reflect.TypeOf(src)
	dstType := reflect.TypeOf(dst).Elem()

	key := [2]reflect.Type{srcType, dstType}

	mu.RLock()
	fn, ok := registry[key]
	mu.RUnlock()
	if !ok {
		return &NoMapperError{
			SrcType: srcType,
			DstType: dstType,
		}
	}

	err := fn(m.ctx, src, dst)
	if err != nil {
		return &ConversionError{
			SrcType: srcType,
			DstType: dstType,
			Err:     err,
		}
	}
	return nil
}

// GetResolver retrieves the environment variable resolver from the conversion context.
//
// This function is called within conversion functions to access the resolver
// that was attached via WithResolver(). If no resolver was attached, returns nil.
//
// Usage pattern in conversion functions:
//
//	func convertService(ctx context.Context, src *ServiceConfig) (*azdext.ServiceConfig, error) {
//	    resolver := mapper.GetResolver(ctx)
//	    if resolver != nil {
//	        // Expand environment variables in configuration
//	        expandedImage, err := src.Image.Envsubst(func(key string) string {
//	            return resolver(key)
//	        })
//	        if err != nil {
//	            return nil, err
//	        }
//	        return &azdext.ServiceConfig{Image: expandedImage}, nil
//	    }
//	    // Fallback when no resolver available
//	    return &azdext.ServiceConfig{Image: string(src.Image)}, nil
//	}
//
// Returns nil if no resolver was attached to the mapper context.
func GetResolver(ctx context.Context) Resolver {
	if resolver, ok := ctx.Value(resolverKey).(func(string) string); ok {
		return Resolver(resolver)
	}
	return nil
}

// ResolverFromContext retrieves the resolver from context with explicit presence indication.
//
// This is similar to GetResolver but returns a boolean indicating whether a resolver
// was actually attached to the context. Use this when you need to distinguish between
// "no resolver attached" and "resolver attached but returns empty strings".
//
// Example:
//
//	func convertWithOptionalResolver(ctx context.Context, src *Config) (*Proto, error) {
//	    if resolver, hasResolver := mapper.ResolverFromContext(ctx); hasResolver {
//	        // Resolver was explicitly attached, use it even if it returns ""
//	        expanded := resolver("SOME_VAR")
//	        return &Proto{Value: expanded}, nil
//	    } else {
//	        // No resolver attached, use different strategy
//	        return &Proto{Value: "default"}, nil
//	    }
//	}
//
// Returns:
//   - resolver: The resolver function if attached, nil otherwise
//   - ok: true if a resolver was attached, false otherwise
func ResolverFromContext(ctx context.Context) (Resolver, bool) {
	if resolver, ok := ctx.Value(resolverKey).(func(string) string); ok {
		return Resolver(resolver), true
	}
	return nil, false
}

// Clear removes all registered mappers from the global registry.
//
// This function is primarily intended for testing to ensure test isolation.
// Each test can start with a clean mapper registry without interference
// from other tests or global registrations.
//
// WARNING: This affects the global mapper registry. Use with caution in
// production code as it will remove ALL registered converters.
//
// Example test usage:
//
//	func TestMapper(t *testing.T) {
//	    // Ensure clean state
//	    mapper.Clear()
//	    defer mapper.Clear() // Clean up after test
//
//	    // Register test-specific mappers
//	    mapper.MustRegister(func(ctx context.Context, src TestType) (TargetType, error) {
//	        return TargetType{Value: src.Value}, nil
//	    })
//
//	    // Run test...
//	}
func Clear() {
	mu.Lock()
	defer mu.Unlock()
	registry = make(map[[2]reflect.Type]MapperFunc)
}
