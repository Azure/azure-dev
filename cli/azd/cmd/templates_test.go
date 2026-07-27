// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestGetCmdTemplateHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "template"}
	result := getCmdTemplateHelpDescription(cmd)
	require.Contains(t, result, "template")
}

func TestGetCmdTemplateHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "template"}
	result := getCmdTemplateHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdTemplateSourceHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "source"}
	result := getCmdTemplateSourceHelpDescription(cmd)
	require.Contains(t, result, "source")
}

func TestGetCmdTemplateSourceHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "source"}
	result := getCmdTemplateSourceHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdTemplateSourceAddHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "add"}
	result := getCmdTemplateSourceAddHelpDescription(cmd)
	require.NotEmpty(t, result)
}

func TestGetCmdTemplateSourceAddHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "add"}
	result := getCmdTemplateSourceAddHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestNewTemplateListCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateListCmd()
	require.Contains(t, cmd.Use, "list")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateShowCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateShowCmd()
	require.Contains(t, cmd.Use, "show")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateSourceListCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceListCmd()
	require.Contains(t, cmd.Use, "list")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateSourceAddCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceAddCmd()
	require.Contains(t, cmd.Use, "add")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateSourceRemoveCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceRemoveCmd()
	require.Contains(t, cmd.Use, "remove")
	require.NotEmpty(t, cmd.Short)
}

func TestNewTemplateListFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "list"}
	flags := newTemplateListFlags(cmd)
	require.NotNil(t, flags)
	// Verify source flag is registered
	f := cmd.Flags().Lookup("source")
	require.NotNil(t, f)
}

// ---------------------------------------------------------------------------
// mockSourceManager implements templates.SourceManager for testing
// ---------------------------------------------------------------------------

type mockTemplateSourceManager struct {
	mock.Mock
}

func (m *mockTemplateSourceManager) List(ctx context.Context) ([]*templates.SourceConfig, error) {
	args := m.Called(ctx)
	return args.Get(0).([]*templates.SourceConfig), args.Error(1)
}

func (m *mockTemplateSourceManager) Get(ctx context.Context, name string) (*templates.SourceConfig, error) {
	args := m.Called(ctx, name)
	return args.Get(0).(*templates.SourceConfig), args.Error(1)
}

func (m *mockTemplateSourceManager) Add(ctx context.Context, key string, source *templates.SourceConfig) error {
	args := m.Called(ctx, key, source)
	return args.Error(0)
}

func (m *mockTemplateSourceManager) Remove(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func (m *mockTemplateSourceManager) CreateSource(
	ctx context.Context, source *templates.SourceConfig,
) (templates.Source, error) {
	args := m.Called(ctx, source)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(templates.Source), args.Error(1)
}

// ---------------------------------------------------------------------------
// templateSourceListAction tests
// ---------------------------------------------------------------------------

func Test_TemplateSourceListAction_Success(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("List", mock.Anything).Return([]*templates.SourceConfig{
		{Key: "default", Name: "Default", Type: "resource"},
		{Key: "awesome-azd", Name: "Awesome AZD", Type: "awesome-azd", Location: "https://example.com"},
	}, nil)

	var buf bytes.Buffer
	formatter := &output.JsonFormatter{}
	action := newTemplateSourceListAction(formatter, &buf, srcMgr)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "default")
	srcMgr.AssertCalled(t, "List", mock.Anything)
}

func Test_TemplateSourceListAction_ListError(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("List", mock.Anything).Return(([]*templates.SourceConfig)(nil), fmt.Errorf("config error"))

	var buf bytes.Buffer
	formatter := &output.NoneFormatter{}
	action := newTemplateSourceListAction(formatter, &buf, srcMgr)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list template sources")
}

func Test_TemplateSourceListAction_EmptyList(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("List", mock.Anything).Return([]*templates.SourceConfig{}, nil)

	var buf bytes.Buffer
	formatter := &output.JsonFormatter{}
	action := newTemplateSourceListAction(formatter, &buf, srcMgr)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_TemplateSourceListAction_JsonFormat(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("List", mock.Anything).Return([]*templates.SourceConfig{
		{Key: "default", Name: "Default", Type: "resource"},
	}, nil)

	var buf bytes.Buffer
	formatter := &output.JsonFormatter{}
	action := newTemplateSourceListAction(formatter, &buf, srcMgr)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "default")
}

// ---------------------------------------------------------------------------
// templateSourceRemoveAction tests
// ---------------------------------------------------------------------------

func Test_TemplateSourceRemoveAction_Success(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("Remove", mock.Anything, "my-source").Return(nil)

	console := mockinput.NewMockConsole()
	action := newTemplateSourceRemoveAction(srcMgr, console, []string{"my-source"})

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Message.Header, "Removed azd template source my-source")
}

func Test_TemplateSourceRemoveAction_Error(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("Remove", mock.Anything, "bad-source").Return(fmt.Errorf("not found"))

	console := mockinput.NewMockConsole()
	action := newTemplateSourceRemoveAction(srcMgr, console, []string{"bad-source"})

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed removing template source")
}

func Test_TemplateSourceRemoveAction_CaseInsensitive(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("Remove", mock.Anything, "my-source").Return(nil)

	console := mockinput.NewMockConsole()
	action := newTemplateSourceRemoveAction(srcMgr, console, []string{"MY-SOURCE"})

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// templateSourceAddAction tests
// ---------------------------------------------------------------------------

func Test_TemplateSourceAddAction_WellKnownSourceType(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	console := mockinput.NewMockConsole()

	// Using "default" as kind, which matches the well-known SourceDefault type
	flags := &templateSourceAddFlags{kind: "default"}
	action := newTemplateSourceAddAction(flags, console, srcMgr, []string{"my-key"})

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "known source type")
}

func Test_TemplateSourceAddAction_CustomSource_Success(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("CreateSource", mock.Anything, mock.Anything).Return(nil, nil)
	srcMgr.On("Add", mock.Anything, "my-custom", mock.Anything).Return(nil)

	console := mockinput.NewMockConsole()
	flags := &templateSourceAddFlags{kind: "url", location: "https://example.com/templates.json", name: "My Custom"}
	action := newTemplateSourceAddAction(flags, console, srcMgr, []string{"my-custom"})

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Message.Header, "Added azd template source my-custom")
}

func Test_TemplateSourceAddAction_InvalidSourceType(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("CreateSource", mock.Anything, mock.Anything).
		Return(nil, templates.ErrSourceTypeInvalid)

	console := mockinput.NewMockConsole()
	flags := &templateSourceAddFlags{kind: "invalid-type", location: "x"}
	action := newTemplateSourceAddAction(flags, console, srcMgr, []string{"my-key"})

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func Test_TemplateSourceAddAction_CreateSourceError(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("CreateSource", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("network error"))

	console := mockinput.NewMockConsole()
	flags := &templateSourceAddFlags{kind: "url", location: "https://bad.com"}
	action := newTemplateSourceAddAction(flags, console, srcMgr, []string{"my-key"})

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template source validation failed")
}

func Test_TemplateSourceAddAction_AddError(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("CreateSource", mock.Anything, mock.Anything).Return(nil, nil)
	srcMgr.On("Add", mock.Anything, "my-key", mock.Anything).Return(fmt.Errorf("duplicate"))

	console := mockinput.NewMockConsole()
	flags := &templateSourceAddFlags{kind: "url", location: "https://example.com"}
	action := newTemplateSourceAddAction(flags, console, srcMgr, []string{"my-key"})

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed adding template source")
}

func Test_TemplateSourceAddAction_WellKnownKey(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	// When key is "default", it's a well-known source key, so no CreateSource needed
	srcMgr.On("Add", mock.Anything, "default", mock.Anything).Return(nil)

	console := mockinput.NewMockConsole()
	flags := &templateSourceAddFlags{}
	action := newTemplateSourceAddAction(flags, console, srcMgr, []string{"default"})

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	srcMgr.AssertNotCalled(t, "CreateSource", mock.Anything, mock.Anything)
}

// ---------------------------------------------------------------------------
// templateSourceListAction - Table format test
// ---------------------------------------------------------------------------

func Test_TemplateSourceListAction_TableFormat(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("List", mock.Anything).Return([]*templates.SourceConfig{
		{Key: "default", Name: "Default", Type: "resource"},
		{Key: "custom", Name: "My Templates", Type: "url", Location: "https://example.com"},
	}, nil)

	var buf bytes.Buffer
	formatter := &output.TableFormatter{}
	action := newTemplateSourceListAction(formatter, &buf, srcMgr)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "default")
}

// (removed cobra_cmd_noop since GetCommandFormatter has different signature)

// ---------------------------------------------------------------------------
// templateListAction.Run — tests for the template list (not source list)
// ---------------------------------------------------------------------------

func Test_TemplateSourceListAction_SingleItem(t *testing.T) {
	t.Parallel()
	srcMgr := &mockTemplateSourceManager{}
	srcMgr.On("List", mock.Anything).Return([]*templates.SourceConfig{
		{Key: "only-one", Name: "Only Source", Type: "file", Location: "/tmp/templates"},
	}, nil)

	var buf bytes.Buffer
	formatter := &output.JsonFormatter{}
	action := newTemplateSourceListAction(formatter, &buf, srcMgr)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "only-one")
}

// ---------------------------------------------------------------------------
// getCmdTemplateHelpFooter
// ---------------------------------------------------------------------------

func Test_GetCmdTemplateHelpFooter(t *testing.T) {
	t.Parallel()
	footer := getCmdTemplateHelpFooter(nil)
	assert.NotEmpty(t, footer)
	assert.Contains(t, footer, "template list")
}

// ---------------------------------------------------------------------------
// getCmdTemplateHelpDescription
// ---------------------------------------------------------------------------

func Test_GetCmdTemplateSourceHelpDescription(t *testing.T) {
	t.Parallel()
	desc := getCmdTemplateSourceHelpDescription(nil)
	assert.NotEmpty(t, desc)
}

func Test_NewTemplateListAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &templateListFlags{}
	formatter := &output.JsonFormatter{}
	a := newTemplateListAction(flags, formatter, io.Discard, nil)
	ta := a.(*templateListAction)
	require.Same(t, flags, ta.flags)
	require.Same(t, formatter, ta.formatter)
}

func Test_GetCmdTemplateSourceHelpFooter3(t *testing.T) {
	t.Parallel()
	footer := getCmdTemplateSourceHelpFooter(nil)
	assert.NotEmpty(t, footer)
}

func Test_NewTemplateShowAction(t *testing.T) {
	t.Parallel()
	action := newTemplateShowAction(nil, nil, nil, []string{"my-template"})
	require.NotNil(t, action)
}

func Test_NewTemplateListFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newTemplateListFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewTemplateSourceAddFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newTemplateSourceAddFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewTemplateListCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateListCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "list")
}

func Test_NewTemplateShowCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateShowCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "show")
}

func Test_NewTemplateSourceListCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceListCmd()
	require.NotNil(t, cmd)
}

func Test_NewTemplateSourceAddCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceAddCmd()
	require.NotNil(t, cmd)
}

func Test_NewTemplateSourceRemoveCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceRemoveCmd()
	require.NotNil(t, cmd)
}
