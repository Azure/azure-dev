// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package localio

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func platformPermissions(file *os.File, private bool) error {
	var stat unix.Statfs_t
	fd := file.Fd()
	if fd > math.MaxInt {
		return errors.New("output directory handle is invalid")
	}
	if err := unix.Fstatfs(int(fd), &stat); err != nil {
		return err
	}
	name := unix.ByteSliceToString(stat.Fstypename[:])
	if name != "apfs" && name != "hfs" {
		return errors.New("output filesystem permission guarantees are unsupported")
	}
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	buffer := make([]byte, 64*1024)
	//nolint:gosec // The syscall requires pointers to the fixed attrlist and live output buffer.
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, file.Fd(),
		uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, 0)
	runtime.KeepAlive(file)
	if errno != 0 {
		return errno
	}
	return verifyDarwinACL(buffer, private)
}

func verifyDarwinACL(buffer []byte, private bool) error {
	invalid := errors.New("output ACL guarantees could not be verified")
	if len(buffer) < 12 {
		return invalid
	}
	size := int(binary.LittleEndian.Uint32(buffer))
	if size < 12 || size > len(buffer) {
		return invalid
	}
	rawOffset := binary.LittleEndian.Uint32(buffer[4:])
	if rawOffset > math.MaxInt32 {
		return invalid
	}
	offset := 4 + int(rawOffset)
	length := int(binary.LittleEndian.Uint32(buffer[8:]))
	if length == 0 {
		return nil
	}
	if offset < 12 || length < 44 || offset > size-length {
		return invalid
	}
	security := buffer[offset : offset+length]
	if binary.LittleEndian.Uint32(security) != 0x012cc16d {
		return invalid
	}
	count := binary.LittleEndian.Uint32(security[36:])
	if count == ^uint32(0) {
		return nil
	}
	if int64(count)*24 > int64(len(security))-44 {
		return invalid
	}
	for index := range int(count) {
		entry := security[44+index*24:]
		flags := binary.LittleEndian.Uint32(entry[16:])
		rights := binary.LittleEndian.Uint32(entry[20:])
		switch flags & 0xf {
		case 2: // Deny entries do not grant access.
			continue
		case 1:
			// Conservatively reject mutation grants, even for named trusted accounts.
			// Private staging also rejects inheritable/read grants before creating data.
			const mutations = 1<<2 | 1<<4 | 1<<5 | 1<<6 | 1<<8 | 1<<10 | 1<<12 | 1<<13 | 1<<21 | 1<<23
			if private || (flags&(1<<8) == 0 && rights&mutations != 0) {
				return invalid
			}
		default:
			return invalid
		}
	}
	return nil
}
