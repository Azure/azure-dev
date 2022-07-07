package internal

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetUserAgent(t *testing.T) {
	orig := os.Getenv("AZURE_DEV_USER_AGENT")
	defer func() { os.Setenv("AZURE_DEV_USER_AGENT", orig) }()

	version := GetVersionNumber()
	require.NotEmpty(t, version)

	os.Setenv("AZURE_DEV_USER_AGENT", "")

	require.Equal(t, fmt.Sprintf("azdev/%s", version), FormatUserAgent([]string{}))
	require.Equal(t, fmt.Sprintf("azdev/%s", version), FormatUserAgent(nil))
	require.Equal(t, fmt.Sprintf("azdev/%s extra values", version), FormatUserAgent([]string{"extra", "values"}))

	os.Setenv("AZURE_DEV_USER_AGENT", "dev_user_agent")

	require.Equal(t, fmt.Sprintf("azdev/%s dev_user_agent", version), FormatUserAgent([]string{}))
	require.Equal(t, fmt.Sprintf("azdev/%s dev_user_agent", version), FormatUserAgent(nil))
	require.Equal(t, fmt.Sprintf("azdev/%s dev_user_agent extra values", version), FormatUserAgent([]string{"extra", "values"}))
}

func TestFormatTemplateForUserAgent(t *testing.T) {
	require.Equal(t, "azdtempl/[none]", FormatTemplateForUserAgent(""))
	require.Equal(t, "azdtempl/todo-python-mongo", FormatTemplateForUserAgent("todo-python-mongo"))
	require.Equal(t, "azdtempl/todo-csharp-sql@0.0.1-beta", FormatTemplateForUserAgent("todo-csharp-sql@0.0.1-beta"))
}
