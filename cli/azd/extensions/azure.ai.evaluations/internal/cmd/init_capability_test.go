// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestInitSelectsCapabilityBeforeOneMutation(t *testing.T) {
	for _, mode := range []string{"capable", "old explicit", "old unavailable RPC", "old absent beta service"} {
		for _, fails := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "success", true: "failure"}[fails], func(t *testing.T) {
				h := newInitHarnessWithCapabilities(t, nil, nil, mode != "old absent beta service")
				h.project.supportsAck = mode == "capable"
				if mode == "old unavailable RPC" {
					h.project.capabilityErr = status.Error(codes.Unimplemented, "unknown method")
				}
				path := filepath.Join(h.dir, "quality.yml")
				h.project.setAddServiceHandler(func(ctx context.Context, _ *azdext.AddServiceRequest) error {
					if !fails {
						return nil
					}
					if mode != "capable" {
						// Even an old host's custom trailer must not be treated as the typed capability.
						incoming, _ := metadata.FromIncomingContext(ctx)
						assert.Empty(t, incoming.Get("azd-project-add-service-operation"))
						if err := grpc.SetTrailer(ctx, metadata.Pairs(
							"azd-project-add-service-save-failed", "untrusted")); err != nil {
							return err
						}
					}
					return os.ErrPermission
				})
				_, err := executeConversationInit(t, "--path", path, "--name", "quality", "--source", "traces",
					"--target", "agent", "--judge-model", "judge", "--no-prompt", "-o", "json")
				if !fails {
					require.NoError(t, err)
					assert.FileExists(t, path)
					assertOneInitCompleted(t, h, "traces")
				} else if mode == "capable" {
					require.ErrorContains(t, err, "was rolled back")
					assert.NoFileExists(t, path)
				} else {
					require.ErrorContains(t, err, "could not safely roll back")
					assert.FileExists(t, path)
				}
				assert.Equal(t, 1, h.project.wiringAttempts(), "no path may replay the mutation")
				h.project.mu.Lock()
				assert.Equal(t, map[bool]int{false: 0, true: 1}[mode == "capable"], h.project.betaCalls)
				h.project.mu.Unlock()
			})
		}
	}
}

type malformedCapabilitiesCodec struct{ malformedSaveResponseCodec }

func (malformedCapabilitiesCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("not a protobuf message: %T", value)
	}
	if message.ProtoReflect().Descriptor().Name() == "GetAddServiceCapabilitiesResponse" {
		return []byte{0x0e}, nil
	}
	return proto.Marshal(message)
}

func TestInitMalformedCapabilityResponseNeverMutates(t *testing.T) {
	h := newInitHarnessWithOptions(t, nil, []grpc.ServerOption{grpc.ForceServerCodec(malformedCapabilitiesCodec{})})
	path := filepath.Join(h.dir, "quality.yml")
	_, err := executeConversationInit(t, "--path", path, "--name", "quality", "--source", "traces",
		"--target", "agent", "--judge-model", "judge", "--no-prompt", "-o", "json")
	require.ErrorContains(t, err, "failed to unmarshal")
	assert.Zero(t, h.project.wiringAttempts())
	assert.NoFileExists(t, path)
}

func TestInitCapabilityErrorsNeverMutateOrFallback(t *testing.T) {
	for _, code := range []codes.Code{codes.Canceled, codes.DeadlineExceeded, codes.Unauthenticated,
		codes.PermissionDenied, codes.Unavailable, codes.Internal} {
		t.Run(code.String(), func(t *testing.T) {
			h := newInitHarness(t, nil)
			h.project.capabilityErr = status.Error(code, "capability not established")
			path := filepath.Join(h.dir, "quality.yml")
			_, err := executeConversationInit(t, "--path", path, "--name", "quality", "--source", "traces",
				"--target", "agent", "--judge-model", "judge", "--no-prompt", "-o", "json")
			require.ErrorContains(t, err, "capability not established")
			assert.Zero(t, h.project.wiringAttempts())
			assert.Empty(t, h.usage.reported())
			assert.NoFileExists(t, path, "a failed read-only probe cannot leave a root save in flight")
		})
	}
}

func TestInitMutationErrorsNeverReplayOrTrustTransportAcknowledgments(t *testing.T) {
	for _, code := range []codes.Code{codes.Canceled, codes.DeadlineExceeded, codes.Unauthenticated,
		codes.PermissionDenied, codes.Unavailable, codes.Unimplemented} {
		t.Run(code.String(), func(t *testing.T) {
			h := newInitHarness(t, status.Error(code, "mutation failed"))
			if code == codes.Unimplemented {
				h.project.setSaveFailureAcknowledgement(false)
			}
			path := filepath.Join(h.dir, "quality.yml")
			_, err := executeConversationInit(t, "--path", path, "--name", "quality", "--source", "traces",
				"--target", "agent", "--judge-model", "judge", "--no-prompt", "-o", "json")
			require.ErrorContains(t, err, "could not safely roll back")
			assert.FileExists(t, path)
			assert.Equal(t, 1, h.project.wiringAttempts())
			h.project.mu.Lock()
			assert.Equal(t, 1, h.project.betaCalls)
			h.project.mu.Unlock()
			assert.Empty(t, h.usage.reported())
		})
	}
}
