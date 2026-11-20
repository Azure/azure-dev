// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type addTestConsole struct {
	input.Console
	handles input.ConsoleHandles
}

func (c addTestConsole) Handles() input.ConsoleHandles {
	return c.handles
}

func TestAddProviderDispatch(t *testing.T) {
	tests := []struct {
		name        string
		noProject   bool
		noPrompt    bool
		noItems     bool
		startupErr  error
		wantErr     string
		wantInvoked bool
	}{
		{name: "select without default environment", wantInvoked: true},
		{name: "select without project", noProject: true, wantInvoked: true},
		{name: "no prompt", noPrompt: true, wantErr: "requires interactive input"},
		{name: "startup failure", startupErr: errors.New("listener failed"), wantErr: "listener failed"},
		{name: "empty provider falls back to legacy", noItems: true, noProject: true, wantErr: "no project exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", configDir)
			t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
			t.Setenv("AZD_NON_INTERACTIVE", "false")
			projectDir := t.TempDir()
			t.Chdir(projectDir)
			if !tt.noProject {
				require.NoError(t, os.WriteFile("azure.yaml", []byte("name: test-add\ninfra:\n  provider: custom\n"), 0o600))
			}

			extensionPath := filepath.Join("extensions", "test.add", "bin", "test-add")
			fullPath := filepath.Join(configDir, extensionPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o700))
			require.NoError(t, os.WriteFile(fullPath, []byte("mock"), 0o600))

			mockCtx := mocks.NewMockContext(t.Context())
			manager := newExtensionActionTestManager(t, mockCtx, &extensions.Extension{
				Id: "test.add", Path: extensionPath, Version: "1.0.0",
				Capabilities: []extensions.CapabilityType{extensions.AddProviderCapability},
			})
			args := []string{"add"}
			if tt.noPrompt {
				args = append(args, "--no-prompt")
			}
			options := &internal.GlobalCommandOptions{}
			require.NoError(t, ParseGlobalFlags(args, options))
			ioc.RegisterInstance(mockCtx.Container, options)
			root := NewRootCmd(false, nil, mockCtx.Container)
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			ioc.RegisterInstance(mockCtx.Container, manager)
			ioc.RegisterInstance(mockCtx.Container, extensions.NewRunner(mockCtx.CommandRunner))
			mockCtx.Container.MustRegisterScoped(func() input.Console {
				handles := mockCtx.Console.Handles()
				handles.Stdout = root.OutOrStdout()
				handles.Stderr = root.ErrOrStderr()
				return addTestConsole{Console: mockCtx.Console, handles: handles}
			})

			var prompted bool
			mockCtx.Console.WhenSelect(func(options input.ConsoleOptions) bool {
				return options.Message == "What would you like to add?"
			}).RespondFn(func(options input.ConsoleOptions) (any, error) {
				prompted = true
				require.Equal(t, []string{"Same (test.add: a)", "Same (test.add: b)"}, options.Options)
				require.Nil(t, options.DefaultValue)
				return 1, nil
			})

			invoked := make(chan string, 1)
			stopped := make(chan struct{})
			items := []*v1beta.AddItem{{Id: "b", DisplayName: "Same"}, {Id: "a", DisplayName: "Same"}}
			if tt.noItems {
				items = nil
			}
			mockCtx.CommandRunner.When(func(args exec.RunArgs, _ string) bool { return args.Cmd == fullPath }).
				RespondContextFn(func(ctx context.Context, args exec.RunArgs) (exec.RunResult, error) {
					defer close(stopped)
					if tt.startupErr != nil {
						return exec.RunResult{}, tt.startupErr
					}
					return exec.RunResult{}, fakeAddListener(ctx, args, items, invoked)
				})

			root.SetArgs(args)
			err := root.ExecuteContext(t.Context())
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
			require.Equal(t, tt.wantInvoked, prompted)
			if tt.wantInvoked {
				require.Len(t, invoked, 1)
				require.Equal(t, "b", <-invoked)
			} else {
				require.Empty(t, invoked)
			}
			select {
			case <-stopped:
			default:
				t.Fatal("extension listener was not stopped")
			}
			if tt.wantInvoked {
				require.Equal(t, "Saved resources configuration\nRun azd provision\n", stdout.String())
				require.Equal(t, "Resource name is invalid\n", stderr.String())

				extension, err := manager.GetInstalled(extensions.FilterOptions{Id: "test.add"})
				require.NoError(t, err)
				for _, writer := range []io.Writer{extension.StdOut(), extension.StdErr()} {
					_, err := io.WriteString(writer, "after cleanup\n")
					require.NoError(t, err)
				}
				require.NotContains(t, stdout.String(), "after cleanup")
				require.NotContains(t, stderr.String(), "after cleanup")
			} else {
				require.NotContains(t, stdout.String(), "listener startup")
				require.NotContains(t, stderr.String(), "listener startup")
			}
			require.NoDirExists(t, filepath.Join(projectDir, ".azure"))
		})
	}
}

func fakeAddListener(ctx context.Context, args exec.RunArgs, items []*v1beta.AddItem, invoked chan<- string) error {
	for _, writer := range []io.Writer{args.StdOut, args.Stderr} {
		if _, err := io.WriteString(writer, "listener startup\n"); err != nil {
			return err
		}
	}
	env := map[string]string{}
	for _, value := range args.Env {
		key, value, _ := strings.Cut(value, "=")
		env[key] = value
	}
	conn, err := grpc.NewClient(env["AZD_SERVER"], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", env["AZD_ACCESS_TOKEN"])
	stream, err := v1beta.NewAddProviderServiceClient(conn).Stream(ctx)
	if err != nil {
		return err
	}
	err = stream.Send(&v1beta.AddProviderMessage{
		RequestId: "register",
		MessageType: &v1beta.AddProviderMessage_RegisterRequest{
			RegisterRequest: &v1beta.RegisterAddProviderRequest{Items: items},
		},
	})
	if err != nil {
		return err
	}
	if ack, err := stream.Recv(); err != nil || ack.GetRegisterResponse() == nil {
		return fmt.Errorf("receiving add provider registration acknowledgement: %v", err)
	}
	if _, err := v1beta.NewExtensionServiceClient(conn).Ready(ctx, &v1beta.ReadyRequest{}); err != nil {
		return err
	}
	msg, err := stream.Recv()
	if err != nil {
		return nil // The host may shut down without a selection.
	}

	invoked <- msg.GetAddRequest().GetId()
	if _, err := io.WriteString(args.Stderr, "Resource name is invalid\n"); err != nil {
		return err
	}
	if _, err := io.WriteString(args.StdOut, "Saved resources configuration\n"); err != nil {
		return err
	}
	if err := stream.Send(&v1beta.AddProviderMessage{
		RequestId: msg.GetRequestId(),
		MessageType: &v1beta.AddProviderMessage_AddResponse{
			AddResponse: &v1beta.AddItemResponse{},
		},
	}); err != nil {
		return err
	}
	// Simulate the process IO copy delivering the last bytes after the gRPC response.
	_, _ = stream.Recv()
	_, err = io.WriteString(args.StdOut, "Run azd provision\n")
	return err
}
