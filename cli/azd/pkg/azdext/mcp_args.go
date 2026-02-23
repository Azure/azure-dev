package azdext

import (
	"fmt"
	"math"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolArgs wraps parsed MCP tool arguments for typed access.
type ToolArgs struct {
	raw map[string]interface{}
}

// ParseToolArgs extracts the arguments map from an MCP CallToolRequest.
func ParseToolArgs(request mcp.CallToolRequest) ToolArgs {
	args := request.GetArguments()
	if args == nil {
		return ToolArgs{raw: make(map[string]interface{})}
	}
	return ToolArgs{raw: args}
}

// RequireString returns a string argument or an error if missing/wrong type.
func (a ToolArgs) RequireString(key string) (string, error) {
	v, ok := a.raw[key]
	if !ok {
		return "", fmt.Errorf("required argument %q not found", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %q is not a string, got %T", key, v)
	}
	return s, nil
}

// OptionalString returns a string argument or the default if missing.
func (a ToolArgs) OptionalString(key, defaultValue string) string {
	v, ok := a.raw[key]
	if !ok {
		return defaultValue
	}
	s, ok := v.(string)
	if !ok {
		return defaultValue
	}
	return s
}

// RequireInt returns an int argument or an error if missing/wrong type.
// Note: JSON numbers come as float64, so convert appropriately.
func (a ToolArgs) RequireInt(key string) (int, error) {
	v, ok := a.raw[key]
	if !ok {
		return 0, fmt.Errorf("required argument %q not found", key)
	}
	switch n := v.(type) {
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("argument %q is not an integer, got %v", key, n)
		}
		if n > math.MaxInt || n < math.MinInt {
			return 0, fmt.Errorf("argument %q overflows int: %v", key, n)
		}
		return int(n), nil
	case int:
		return n, nil
	default:
		return 0, fmt.Errorf("argument %q is not a number, got %T", key, v)
	}
}

// OptionalInt returns an int argument or the default if missing.
func (a ToolArgs) OptionalInt(key string, defaultValue int) int {
	v, ok := a.raw[key]
	if !ok {
		return defaultValue
	}
	switch n := v.(type) {
	case float64:
		if n != math.Trunc(n) || n > math.MaxInt || n < math.MinInt {
			return defaultValue
		}
		return int(n)
	case int:
		return n
	default:
		return defaultValue
	}
}

// OptionalBool returns a bool argument or the default if missing.
func (a ToolArgs) OptionalBool(key string, defaultValue bool) bool {
	v, ok := a.raw[key]
	if !ok {
		return defaultValue
	}
	b, ok := v.(bool)
	if !ok {
		return defaultValue
	}
	return b
}

// OptionalFloat returns a float64 argument or the default if missing.
func (a ToolArgs) OptionalFloat(key string, defaultValue float64) float64 {
	v, ok := a.raw[key]
	if !ok {
		return defaultValue
	}
	f, ok := v.(float64)
	if !ok {
		return defaultValue
	}
	return f
}

// Raw returns the underlying argument map.
func (a ToolArgs) Raw() map[string]interface{} {
	return a.raw
}

// Has returns true if the key exists in the arguments.
func (a ToolArgs) Has(key string) bool {
	_, ok := a.raw[key]
	return ok
}
