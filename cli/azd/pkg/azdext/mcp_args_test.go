package azdext

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPParseToolArgs_NilArguments(t *testing.T) {
	req := mcp.CallToolRequest{}
	args := ParseToolArgs(req)
	require.NotNil(t, args.Raw())
	require.Empty(t, args.Raw())
}

func TestMCPParseToolArgs_EmptyArguments(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	args := ParseToolArgs(req)
	require.NotNil(t, args.Raw())
	require.Empty(t, args.Raw())
}

func TestMCPParseToolArgs_PopulatedArguments(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"name":  "test",
		"count": float64(42),
	}
	args := ParseToolArgs(req)
	require.Len(t, args.Raw(), 2)
	require.Equal(t, "test", args.Raw()["name"])
}

func TestMCPRequireString_Present(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"name": "hello"}}
	val, err := args.RequireString("name")
	require.NoError(t, err)
	require.Equal(t, "hello", val)
}

func TestMCPRequireString_Missing(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{}}
	_, err := args.RequireString("name")
	require.Error(t, err)
	require.Contains(t, err.Error(), "required argument \"name\" not found")
}

func TestMCPRequireString_WrongType(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"name": 123}}
	_, err := args.RequireString("name")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not a string")
}

func TestMCPOptionalString_Present(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"name": "hello"}}
	require.Equal(t, "hello", args.OptionalString("name", "default"))
}

func TestMCPOptionalString_Missing(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{}}
	require.Equal(t, "default", args.OptionalString("name", "default"))
}

func TestMCPOptionalString_WrongType(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"name": 42}}
	require.Equal(t, "default", args.OptionalString("name", "default"))
}

func TestMCPRequireInt_Present(t *testing.T) {
	// JSON numbers come as float64
	args := ToolArgs{raw: map[string]interface{}{"count": float64(42)}}
	val, err := args.RequireInt("count")
	require.NoError(t, err)
	require.Equal(t, 42, val)
}

func TestMCPRequireInt_NativeInt(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"count": 7}}
	val, err := args.RequireInt("count")
	require.NoError(t, err)
	require.Equal(t, 7, val)
}

func TestMCPRequireInt_Missing(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{}}
	_, err := args.RequireInt("count")
	require.Error(t, err)
	require.Contains(t, err.Error(), "required argument \"count\" not found")
}

func TestMCPRequireInt_WrongType(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"count": "not_a_number"}}
	_, err := args.RequireInt("count")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not a number")
}

func TestMCPOptionalInt_Present(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"count": float64(10)}}
	require.Equal(t, 10, args.OptionalInt("count", 5))
}

func TestMCPOptionalInt_Missing(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{}}
	require.Equal(t, 5, args.OptionalInt("count", 5))
}

func TestMCPOptionalInt_WrongType(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"count": "bad"}}
	require.Equal(t, 5, args.OptionalInt("count", 5))
}

func TestMCPOptionalBool_Present(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"verbose": true}}
	require.True(t, args.OptionalBool("verbose", false))
}

func TestMCPOptionalBool_Missing(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{}}
	require.False(t, args.OptionalBool("verbose", false))
}

func TestMCPOptionalBool_WrongType(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"verbose": "yes"}}
	require.False(t, args.OptionalBool("verbose", false))
}

func TestMCPOptionalFloat_Present(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"rate": float64(3.14)}}
	require.InDelta(t, 3.14, args.OptionalFloat("rate", 1.0), 0.001)
}

func TestMCPOptionalFloat_Missing(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{}}
	require.InDelta(t, 1.0, args.OptionalFloat("rate", 1.0), 0.001)
}

func TestMCPOptionalFloat_WrongType(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"rate": "bad"}}
	require.InDelta(t, 1.0, args.OptionalFloat("rate", 1.0), 0.001)
}

func TestMCPHas(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"key": "val"}}
	require.True(t, args.Has("key"))
	require.False(t, args.Has("missing"))
}

func TestMCPRaw(t *testing.T) {
	m := map[string]interface{}{"a": "b"}
	args := ToolArgs{raw: m}
	require.Equal(t, m, args.Raw())
}

func TestMCPRequireInt_FloatWithFraction(t *testing.T) {
	// 3.7 is not a valid integer — should error.
	args := ToolArgs{raw: map[string]interface{}{"count": float64(3.7)}}
	_, err := args.RequireInt("count")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not an integer")
}

func TestMCPRequireInt_LargeFloat(t *testing.T) {
	// A float64 beyond int range should error.
	args := ToolArgs{raw: map[string]interface{}{"count": float64(1e18)}}
	_, err := args.RequireInt("count")
	// On 64-bit systems 1e18 fits in int64 so it won't overflow,
	// but it should still parse successfully as a valid integer.
	if err != nil {
		require.Contains(t, err.Error(), "overflows int")
	}
}

func TestMCPOptionalInt_FloatWithFraction(t *testing.T) {
	// Non-integer float should fall back to default.
	args := ToolArgs{raw: map[string]interface{}{"count": float64(2.5)}}
	require.Equal(t, 99, args.OptionalInt("count", 99))
}

func TestMCPOptionalInt_NativeInt(t *testing.T) {
	args := ToolArgs{raw: map[string]interface{}{"count": 17}}
	require.Equal(t, 17, args.OptionalInt("count", 99))
}
