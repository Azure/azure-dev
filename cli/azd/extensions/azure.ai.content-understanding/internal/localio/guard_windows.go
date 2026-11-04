// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package localio

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func guardDirectory(root *os.Root, leaf bool) (_ io.Closer, err error) {
	path, err := windows.UTF16PtrFromString(root.Name())
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(root.Name())
	if len(volume) != 2 || volume[1] != ':' {
		return nil, errors.New("output requires a local ACL-capable volume")
	}
	drive, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil || windows.GetDriveType(drive) != windows.DRIVE_FIXED {
		return nil, errors.New("output requires a local fixed volume")
	}
	handle, err := windows.CreateFile(path, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), root.Name())
	defer func() {
		if err != nil {
			err = errors.Join(err, file.Close())
		}
	}()
	expected, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	actual, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(expected, actual) {
		return nil, errors.New("output directory changed while locking")
	}
	if err := verifyWindowsObject(handle, !leaf); err != nil {
		return nil, err
	}
	// No FILE_SHARE_DELETE: retain locks on the entire validated chain until publication finishes.
	return file, nil
}

func guardFile(root *os.Root, name string) error {
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	return verifyWindowsObject(windows.Handle(file.Fd()), false)
}

func trustedWindowsSIDs() ([]*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	result := []*windows.SID{user.User.Sid}
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid,
	} {
		sid, err := windows.CreateWellKnownSid(kind)
		if err != nil {
			return nil, err
		}
		result = append(result, sid)
	}
	// Windows servicing owns the system volume root on standard installations.
	installer, err := windows.StringToSid("S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464")
	if err != nil {
		return nil, err
	}
	result = append(result, installer)
	return result, nil
}

func verifyWindowsObject(handle windows.Handle, ancestor bool) error {
	var flags uint32
	if err := windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, &flags, nil, 0); err != nil {
		return err
	}
	if flags&windows.FILE_PERSISTENT_ACLS == 0 {
		return errors.New("output volume does not preserve ACLs")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("output must not traverse reparse points")
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	trusted, err := trustedWindowsSIDs()
	if err != nil {
		return err
	}
	return verifyWindowsDescriptor(sd, trusted, ancestor)
}

func verifyWindowsDescriptor(sd *windows.SECURITY_DESCRIPTOR, trusted []*windows.SID, ancestor bool) error {
	unsafeACL := errors.New("output ownership or ACL permits untrusted modification")
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !trustedSID(owner, trusted) {
		return unsafeACL
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return unsafeACL
	}
	const fileDeleteChild = 0x0040
	const mutation = windows.GENERIC_ALL | windows.GENERIC_WRITE |
		windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE | fileDeleteChild |
		windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA
	mask := windows.ACCESS_MASK(mutation)
	if !ancestor {
		mask |= windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA
	}
	for index := range acl.AceCount {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, uint32(index), &ace); err != nil {
			return err
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if ace.Header.AceSize < 16 {
				return unsafeACL
			}
			//nolint:gosec // A validated ACCESS_ALLOWED_ACE stores its SID inline.
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.IsValid() || sid.Len()+8 > int(ace.Header.AceSize) {
				return unsafeACL
			}
			if !trustedSID(sid, trusted) && ace.Mask&mask != 0 {
				return unsafeACL
			}
		default:
			return unsafeACL
		}
	}
	return nil
}

func trustedSID(sid *windows.SID, trusted []*windows.SID) bool {
	for _, candidate := range trusted {
		if sid.Equals(candidate) {
			return true
		}
	}
	return false
}

func secureStaging(root *os.Root) error {
	path, err := windows.UTF16PtrFromString(root.Name())
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(path, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), root.Name())
	defer file.Close()
	expected, err := root.Stat(".")
	if err != nil {
		return err
	}
	actual, err := file.Stat()
	if err != nil || !os.SameFile(expected, actual) {
		return errors.New("staging directory changed before securing its ACL")
	}
	trusted, err := trustedWindowsSIDs()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;FA;;;" + trusted[0].String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return err
	}
	return verifyWindowsObject(windows.Handle(file.Fd()), false)
}
