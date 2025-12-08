// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mapper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearRegistry provides test isolation by clearing all registered mappers
func clearRegistry() {
	Clear()
}

// Test types for validation
type Source struct {
	Name     string
	Template string
	Value    int
}

type Target struct {
	Name      string
	Expanded  string
	Value     int
	Processed bool
}

type ServiceConfig struct {
	Name     string
	ImageTag string
	Endpoint string
	Replicas int
}

type GrpcService struct {
	Name     string
	Image    string
	Endpoint string
	Replicas int
	Ready    bool
}

func TestSimpleMapping(t *testing.T) {
	clearRegistry()

	// Register a simple string-to-string mapper
	Register(func(ctx context.Context, src string) (string, error) {
		return strings.ToUpper(src), nil
	})

	// Test conversion
	var result string
	err := Convert("hello world", &result)

	require.NoError(t, err)
	assert.Equal(t, "HELLO WORLD", result)
}

func TestSimpleMappingWithStruct(t *testing.T) {
	clearRegistry()

	// Register a struct-to-struct mapper
	Register(func(ctx context.Context, src Source) (Target, error) {
		return Target{
			Name:      src.Name,
			Expanded:  src.Template, // No expansion in simple mode
			Value:     src.Value * 2,
			Processed: true,
		}, nil
	})

	// Test conversion
	source := Source{
		Name:     "test",
		Template: "${ENVIRONMENT}",
		Value:    5,
	}

	var result Target
	err := Convert(source, &result)

	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, "${ENVIRONMENT}", result.Expanded) // No expansion
	assert.Equal(t, 10, result.Value)
	assert.True(t, result.Processed)
}

func TestMappingWithResolver(t *testing.T) {
	clearRegistry()

	// Register a resolver-aware mapper that checks for resolver in context
	Register(func(ctx context.Context, src ServiceConfig) (GrpcService, error) {
		grpcSvc := GrpcService{
			Name:     src.Name,
			Replicas: src.Replicas,
			Ready:    true,
		}

		// Use resolver if available in context
		if resolver, hasResolver := ResolverFromContext(ctx); hasResolver {
			grpcSvc.Image = resolver("IMAGE_TAG")
			grpcSvc.Endpoint = resolver("SERVICE_ENDPOINT")
		} else {
			// Fallback values
			grpcSvc.Image = src.ImageTag
			grpcSvc.Endpoint = src.Endpoint
		}

		return grpcSvc, nil
	})

	// Create test resolver
	testResolver := func(key string) string {
		env := map[string]string{
			"IMAGE_TAG":        "myapp:v1.2.3",
			"SERVICE_ENDPOINT": "https://api.example.com",
		}
		if val, exists := env[key]; exists {
			return val
		}
		return ""
	}

	// Test with resolver
	source := ServiceConfig{
		Name:     "my-service",
		ImageTag: "fallback:latest",
		Endpoint: "http://localhost:8080",
		Replicas: 3,
	}

	var result GrpcService
	err := WithResolver(testResolver).Convert(source, &result)

	require.NoError(t, err)
	assert.Equal(t, "my-service", result.Name)
	assert.Equal(t, "myapp:v1.2.3", result.Image)               // From resolver
	assert.Equal(t, "https://api.example.com", result.Endpoint) // From resolver
	assert.Equal(t, 3, result.Replicas)
	assert.True(t, result.Ready)
}

func TestMappingWithoutResolver(t *testing.T) {
	clearRegistry()

	// Register the same resolver-aware mapper
	Register(func(ctx context.Context, src ServiceConfig) (GrpcService, error) {
		grpcSvc := GrpcService{
			Name:     src.Name,
			Replicas: src.Replicas,
			Ready:    true,
		}

		// Use resolver if available in context
		if resolver, hasResolver := ResolverFromContext(ctx); hasResolver {
			grpcSvc.Image = resolver("IMAGE_TAG")
			grpcSvc.Endpoint = resolver("SERVICE_ENDPOINT")
		} else {
			// Fallback values
			grpcSvc.Image = src.ImageTag
			grpcSvc.Endpoint = src.Endpoint
		}

		return grpcSvc, nil
	})

	// Test without resolver (using simple Convert)
	source := ServiceConfig{
		Name:     "my-service",
		ImageTag: "fallback:latest",
		Endpoint: "http://localhost:8080",
		Replicas: 3,
	}

	var result GrpcService
	err := Convert(source, &result) // No resolver

	require.NoError(t, err)
	assert.Equal(t, "my-service", result.Name)
	assert.Equal(t, "fallback:latest", result.Image)          // Fallback value
	assert.Equal(t, "http://localhost:8080", result.Endpoint) // Fallback value
	assert.Equal(t, 3, result.Replicas)
	assert.True(t, result.Ready)
}

func TestMappingWithEnvironmentResolver(t *testing.T) {
	clearRegistry()

	// Set up environment variables for test
	os.Setenv("TEST_IMAGE", "nginx:alpine")
	os.Setenv("TEST_PORT", "9090")
	defer func() {
		os.Unsetenv("TEST_IMAGE")
		os.Unsetenv("TEST_PORT")
	}()

	// Register a mapper that uses environment expansion from context
	Register(func(ctx context.Context, src Source) (Target, error) {
		result := Target{
			Name:      src.Name,
			Value:     src.Value,
			Processed: true,
		}

		if resolver, hasResolver := ResolverFromContext(ctx); hasResolver {
			// Simulate template expansion
			template := src.Template
			if strings.Contains(template, "${TEST_IMAGE}") {
				template = strings.ReplaceAll(template, "${TEST_IMAGE}", resolver("TEST_IMAGE"))
			}
			if strings.Contains(template, "${TEST_PORT}") {
				template = strings.ReplaceAll(template, "${TEST_PORT}", resolver("TEST_PORT"))
			}
			result.Expanded = template
		} else {
			result.Expanded = src.Template
		}

		return result, nil
	})

	// Create environment resolver
	envResolver := func(key string) string {
		return os.Getenv(key)
	}

	// Test with environment resolver
	source := Source{
		Name:     "web-app",
		Template: "docker run ${TEST_IMAGE} -p ${TEST_PORT}:80",
		Value:    42,
	}

	var result Target
	err := WithResolver(envResolver).Convert(source, &result)

	require.NoError(t, err)
	assert.Equal(t, "web-app", result.Name)
	assert.Equal(t, "docker run nginx:alpine -p 9090:80", result.Expanded)
	assert.Equal(t, 42, result.Value)
	assert.True(t, result.Processed)
}

func TestMappingError(t *testing.T) {
	clearRegistry()

	// Register a mapper that returns an error
	Register(func(ctx context.Context, src string) (int, error) {
		if src == "error" {
			return 0, fmt.Errorf("conversion failed for: %s", src)
		}
		return len(src), nil
	})

	// Test successful conversion
	var result int
	err := Convert("hello", &result)
	require.NoError(t, err)
	assert.Equal(t, 5, result)

	// Test error case
	err = Convert("error", &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conversion failed for: error")
}

func TestNoMapperRegistered(t *testing.T) {
	clearRegistry()

	// Try to convert without registering a mapper
	var result string
	err := Convert(123, &result)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mapper registered from int to string")
}

func TestMultipleMappers(t *testing.T) {
	clearRegistry()

	// Register multiple mappers
	Register(func(ctx context.Context, src string) (int, error) {
		return len(src), nil
	})

	Register(func(ctx context.Context, src int) (string, error) {
		return fmt.Sprintf("number-%d", src), nil
	})

	Register(func(ctx context.Context, src string) (Target, error) {
		expanded := src
		if resolver, hasResolver := ResolverFromContext(ctx); hasResolver {
			expanded = resolver("PREFIX") + src
		}
		return Target{
			Name:      "converted",
			Expanded:  expanded,
			Value:     len(src),
			Processed: true,
		}, nil
	})

	// Test string to int
	var intResult int
	err := Convert("hello", &intResult)
	require.NoError(t, err)
	assert.Equal(t, 5, intResult)

	// Test int to string
	var strResult string
	err = Convert(42, &strResult)
	require.NoError(t, err)
	assert.Equal(t, "number-42", strResult)

	// Test string to Target with resolver
	testResolver := func(key string) string {
		if key == "PREFIX" {
			return "test-"
		}
		return ""
	}

	var targetResult Target
	err = WithResolver(testResolver).Convert("data", &targetResult)
	require.NoError(t, err)
	assert.Equal(t, "converted", targetResult.Name)
	assert.Equal(t, "test-data", targetResult.Expanded)
	assert.Equal(t, 4, targetResult.Value)
	assert.True(t, targetResult.Processed)
}

func TestGetResolverFromContext(t *testing.T) {
	testResolver := func(key string) string {
		return "resolved-" + key
	}

	// Test context with resolver
	ctx := context.WithValue(context.Background(), resolverKey, testResolver)
	resolver := GetResolver(ctx)
	require.NotNil(t, resolver, "resolver should not be nil when stored in context")
	assert.Equal(t, "resolved-test", resolver("test"))

	// Test context without resolver
	emptyCtx := context.Background()
	resolver = GetResolver(emptyCtx)
	assert.Nil(t, resolver)
}

func TestResolverFromContext(t *testing.T) {
	testResolver := func(key string) string {
		return "resolved-" + key
	}

	// Test context with resolver
	ctx := context.WithValue(context.Background(), resolverKey, testResolver)
	resolver, hasResolver := ResolverFromContext(ctx)
	require.True(t, hasResolver, "should indicate resolver is present")
	require.NotNil(t, resolver, "resolver should not be nil when present")
	assert.Equal(t, "resolved-test", resolver("test"))

	// Test context without resolver
	emptyCtx := context.Background()
	resolver, hasResolver = ResolverFromContext(emptyCtx)
	assert.False(t, hasResolver, "should indicate resolver is not present")
	assert.Nil(t, resolver, "resolver should be nil when not present")
}

func TestConcurrentAccess(t *testing.T) {
	clearRegistry()

	// Register a simple mapper
	Register(func(ctx context.Context, src string) (string, error) {
		return strings.ToUpper(src), nil
	})

	// Test concurrent access
	const numGoroutines = 100
	results := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			var result string
			err := Convert(fmt.Sprintf("test-%d", id), &result)
			results <- err
		}(i)
	}

	// Check all conversions succeeded
	for i := 0; i < numGoroutines; i++ {
		err := <-results
		assert.NoError(t, err)
	}
}

func TestFluentAPIChaining(t *testing.T) {
	clearRegistry()

	// Register resolver-aware mapper
	Register(func(ctx context.Context, src string) (string, error) {
		if resolver, hasResolver := ResolverFromContext(ctx); hasResolver {
			return resolver("PREFIX") + src + resolver("SUFFIX"), nil
		}
		return src, nil
	})

	// Test fluent API
	testResolver := func(key string) string {
		switch key {
		case "PREFIX":
			return ">> "
		case "SUFFIX":
			return " <<"
		default:
			return ""
		}
	}

	var result string
	err := WithResolver(testResolver).Convert("middle", &result)

	require.NoError(t, err)
	assert.Equal(t, ">> middle <<", result)
}

func TestNoMapperError(t *testing.T) {
	clearRegistry()

	// Try to convert between types that have no registered mapper
	var result string
	err := Convert(42, &result)

	// Should get a NoMapperError
	require.Error(t, err)

	var noMapperErr *NoMapperError
	require.ErrorAs(t, err, &noMapperErr)

	// Verify the error contains the correct type information
	assert.Contains(t, err.Error(), "no mapper registered from int to string")
	assert.Equal(t, "int", noMapperErr.SrcType.String())
	assert.Equal(t, "string", noMapperErr.DstType.String())

	// Test the IsNoMapperError helper function
	assert.True(t, IsNoMapperError(err))
	assert.False(t, IsNoMapperError(nil))
	assert.False(t, IsNoMapperError(fmt.Errorf("some other error")))

	// Test errors.Is() support
	assert.True(t, errors.Is(err, ErrNoMapper))
	assert.False(t, errors.Is(fmt.Errorf("some other error"), ErrNoMapper))
	assert.False(t, errors.Is(nil, ErrNoMapper))
}

func TestNoMapperErrorIs(t *testing.T) {
	// Create two different NoMapperError instances with different types
	err1 := &NoMapperError{
		SrcType: reflect.TypeOf(""),
		DstType: reflect.TypeOf(0),
	}

	err2 := &NoMapperError{
		SrcType: reflect.TypeOf(0),
		DstType: reflect.TypeOf(""),
	}

	// Both should be considered equal to ErrNoMapper via errors.Is()
	assert.True(t, errors.Is(err1, ErrNoMapper))
	assert.True(t, errors.Is(err2, ErrNoMapper))

	// And they should be equal to each other
	assert.True(t, errors.Is(err1, err2))
	assert.True(t, errors.Is(err2, err1))

	// But not equal to other error types
	assert.False(t, errors.Is(err1, fmt.Errorf("different error")))
}

func TestRegisterNilFunction(t *testing.T) {
	clearRegistry()

	// Try to register a nil function
	err := Register[string, string](nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRegistration)
}

func TestRegisterDuplicateRegistration(t *testing.T) {
	clearRegistry()

	// Register a mapper first
	err := Register(func(ctx context.Context, src string) (string, error) {
		return strings.ToUpper(src), nil
	})
	require.NoError(t, err)

	// Try to register another mapper for the same types
	err = Register(func(ctx context.Context, src string) (string, error) {
		return strings.ToLower(src), nil
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicateRegistration)
	assert.Contains(t, err.Error(), "from string to string")
}

func TestMustRegisterPanicsOnNil(t *testing.T) {
	clearRegistry()

	// Should panic when registering nil function
	assert.Panics(t, func() {
		MustRegister[string, string](nil)
	})
}

func TestMustRegisterPanicsOnDuplicate(t *testing.T) {
	clearRegistry()

	// Register a mapper first
	MustRegister(func(ctx context.Context, src string) (string, error) {
		return strings.ToUpper(src), nil
	})

	// Should panic when registering duplicate
	assert.Panics(t, func() {
		MustRegister(func(ctx context.Context, src string) (string, error) {
			return strings.ToLower(src), nil
		})
	})
}

func TestWithResolverNilHandling(t *testing.T) {
	clearRegistry()

	// Register a mapper that uses resolver
	MustRegister(func(ctx context.Context, src string) (string, error) {
		if resolver := GetResolver(ctx); resolver != nil {
			return resolver("PREFIX") + src, nil
		}
		return src, nil
	})

	// Test with nil resolver - should work and return mapper with background context
	mapper := WithResolver(nil)
	require.NotNil(t, mapper)

	var result string
	err := mapper.Convert("test", &result)
	require.NoError(t, err)
	assert.Equal(t, "test", result) // No prefix since resolver is nil
}

func TestConversionError(t *testing.T) {
	clearRegistry()

	// Register a mapper that always fails
	expectedErr := errors.New("conversion failed for testing")
	MustRegister(func(ctx context.Context, src string) (int, error) {
		return 0, expectedErr
	})

	var result int
	err := Convert("test", &result)

	// Verify we get a ConversionError
	require.Error(t, err)
	var convErr *ConversionError
	require.True(t, errors.As(err, &convErr))

	// Check the error message includes type information
	assert.Contains(t, err.Error(), "conversion failed from string to int")
	assert.Contains(t, err.Error(), "conversion failed for testing")

	// Check the wrapped error
	assert.Equal(t, expectedErr, convErr.Err)
	assert.Equal(t, expectedErr, errors.Unwrap(err))

	// Check type information
	assert.Equal(t, reflect.TypeOf(""), convErr.SrcType)
	assert.Equal(t, reflect.TypeOf(0), convErr.DstType)
}

func TestConversionErrorUnwrap(t *testing.T) {
	clearRegistry()

	// Register a mapper that returns a specific error
	innerErr := fmt.Errorf("inner error: %w", errors.New("root cause"))
	MustRegister(func(ctx context.Context, src string) (int, error) {
		return 0, innerErr
	})

	var result int
	err := Convert("test", &result)

	// Verify error chain can be unwrapped
	require.Error(t, err)
	assert.True(t, errors.Is(err, innerErr))

	// Test that errors.Unwrap works
	unwrapped := errors.Unwrap(err)
	assert.Equal(t, innerErr, unwrapped)
}

func TestIsConversionError(t *testing.T) {
	clearRegistry()

	// Register a mapper that fails
	MustRegister(func(ctx context.Context, src string) (int, error) {
		return 0, errors.New("test error")
	})

	var result int
	err := Convert("test", &result)

	// Test the helper function
	require.Error(t, err)
	assert.True(t, IsConversionError(err))
	assert.False(t, IsConversionError(nil))
	assert.False(t, IsConversionError(errors.New("not a conversion error")))

	// Test with NoMapperError (should not be a ConversionError)
	clearRegistry()
	err2 := Convert("unmapped", &result)
	require.Error(t, err2)
	assert.False(t, IsConversionError(err2))
	assert.True(t, IsNoMapperError(err2))
}

func TestConversionErrorIs(t *testing.T) {
	clearRegistry()

	// Test errors.Is() with ConversionError
	t.Run("ConversionError matches ConversionError", func(t *testing.T) {
		MustRegister(func(ctx context.Context, src string) (int, error) {
			return 0, errors.New("test error")
		})

		var result int
		err := Convert("test", &result)
		require.Error(t, err)

		// Should match another ConversionError
		var convErr *ConversionError
		require.True(t, errors.As(err, &convErr))
		assert.True(t, errors.Is(err, &ConversionError{}))
	})

	t.Run("ConversionError matches wrapped error", func(t *testing.T) {
		clearRegistry()

		// Create a specific error to wrap
		specificErr := errors.New("specific validation error")
		MustRegister(func(ctx context.Context, src string) (int, error) {
			return 0, fmt.Errorf("validation failed: %w", specificErr)
		})

		var result int
		err := Convert("test", &result)
		require.Error(t, err)

		// Should match the specific wrapped error
		assert.True(t, errors.Is(err, specificErr))
	})

	t.Run("ConversionError chain with multiple wrapping", func(t *testing.T) {
		clearRegistry()

		// Create a chain of wrapped errors
		rootErr := errors.New("root cause")
		midErr := fmt.Errorf("middle layer: %w", rootErr)

		MustRegister(func(ctx context.Context, src string) (int, error) {
			return 0, fmt.Errorf("top layer: %w", midErr)
		})

		var result int
		err := Convert("test", &result)
		require.Error(t, err)

		// Should match all errors in the chain
		assert.True(t, errors.Is(err, rootErr))
		assert.True(t, errors.Is(err, midErr))
		assert.True(t, errors.Is(err, &ConversionError{}))
	})

	t.Run("ConversionError does not match unrelated errors", func(t *testing.T) {
		clearRegistry()

		MustRegister(func(ctx context.Context, src string) (int, error) {
			return 0, errors.New("conversion error")
		})

		var result int
		err := Convert("test", &result)
		require.Error(t, err)

		// Should not match unrelated errors
		unrelatedErr := errors.New("unrelated error")
		assert.False(t, errors.Is(err, unrelatedErr))
		assert.False(t, errors.Is(err, &NoMapperError{}))
	})

	t.Run("ConversionError matches sentinel error", func(t *testing.T) {
		clearRegistry()

		MustRegister(func(ctx context.Context, src string) (int, error) {
			return 0, errors.New("conversion error")
		})

		var result int
		err := Convert("test", &result)
		require.Error(t, err)

		// Should match the sentinel error
		assert.True(t, errors.Is(err, ErrConversionFailure))
	})
}

func TestConversionErrorTypes(t *testing.T) {
	clearRegistry()

	// Test with different error types
	testCases := []struct {
		name        string
		setupError  error
		expectTypes []reflect.Type
	}{
		{
			name:        "simple error",
			setupError:  errors.New("simple"),
			expectTypes: []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(0)},
		},
		{
			name:        "formatted error",
			setupError:  fmt.Errorf("formatted: %s", "value"),
			expectTypes: []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(0)},
		},
		{
			name:        "wrapped error",
			setupError:  fmt.Errorf("wrapper: %w", errors.New("inner")),
			expectTypes: []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(0)},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clearRegistry()

			MustRegister(func(ctx context.Context, src string) (int, error) {
				return 0, tc.setupError
			})

			var result int
			err := Convert("test", &result)
			require.Error(t, err)

			var convErr *ConversionError
			require.True(t, errors.As(err, &convErr))

			assert.Equal(t, tc.expectTypes[0], convErr.SrcType)
			assert.Equal(t, tc.expectTypes[1], convErr.DstType)
			assert.Equal(t, tc.setupError, convErr.Err)
		})
	}
}
