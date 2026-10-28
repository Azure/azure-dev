// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package localio

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestLinuxACLWriteMaskIsRejected(t *testing.T) {
	directory := testutil.TempDir(t)
	_, err := Check(filepath.Join(directory, "result"))
	require.NoError(t, err)
	acl := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(acl, 2)
	for index, entry := range []struct {
		tag, permission uint16
		id              uint32
	}{{1, 7, ^uint32(0)}, {2, 7, 65534}, {4, 0, ^uint32(0)}, {16, 7, ^uint32(0)}, {32, 0, ^uint32(0)}} {
		offset := 4 + index*8
		binary.LittleEndian.PutUint16(acl[offset:], entry.tag)
		binary.LittleEndian.PutUint16(acl[offset+2:], entry.permission)
		binary.LittleEndian.PutUint32(acl[offset+4:], entry.id)
	}
	err = unix.Setxattr(directory, "system.posix_acl_access", acl, 0)
	if errors.Is(err, unix.ENOTSUP) {
		t.Skip("filesystem does not support POSIX ACLs")
	}
	require.NoError(t, err)
	require.Error(t, Write(filepath.Join(directory, "result"), []byte("private"), false))
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	assert.Empty(t, entries)
}
