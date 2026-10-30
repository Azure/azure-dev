// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package localio

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDarwinACLDecoding(t *testing.T) {
	for _, tt := range []struct {
		name    string
		count   uint32
		flags   uint32
		rights  uint32
		private bool
		allowed bool
	}{
		{"no ACL", ^uint32(0), 0, 0, true, true},
		{"empty ACL", 0, 0, 0, true, true},
		{"deny", 1, 2, ^uint32(0), true, true},
		{"read", 1, 1, 1 << 1, false, true},
		{"private read grant", 1, 1, 1 << 1, true, false},
		{"write", 1, 1, 1 << 2, false, false},
		{"delete", 1, 1, 1 << 4, false, false},
		{"generic all", 1, 1, 1 << 21, false, false},
		{"generic write", 1, 1, 1 << 23, false, false},
		{"generic read", 1, 1, 1 << 24, false, true},
		{"private generic read", 1, 1, 1 << 24, true, false},
		{"inherit only", 1, 1 | 1<<8, 1 << 2, false, true},
		{"private inherited grant", 1, 1 | 1<<8, 1 << 1, true, false},
		{"unknown ACE", 1, 9, 0, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const size = 12 + 44 + 24
			buffer := make([]byte, size)
			binary.LittleEndian.PutUint32(buffer, size)
			binary.LittleEndian.PutUint32(buffer[4:], 8)
			binary.LittleEndian.PutUint32(buffer[8:], size-12)
			binary.LittleEndian.PutUint32(buffer[12:], 0x012cc16d)
			binary.LittleEndian.PutUint32(buffer[12+36:], tt.count)
			binary.LittleEndian.PutUint32(buffer[12+44+16:], tt.flags)
			binary.LittleEndian.PutUint32(buffer[12+44+20:], tt.rights)
			err := verifyDarwinACL(buffer, tt.private)
			if tt.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Error(t, verifyDarwinACL(buffer[:11], tt.private))
			binary.LittleEndian.PutUint32(buffer[4:], ^uint32(0))
			require.Error(t, verifyDarwinACL(buffer, tt.private))
		})
	}
}
