package osutil

import "os"

const (
	PermissionDirectory      os.FileMode = 0755
	PermissionExecutableFile os.FileMode = 0755
	PermissionFile           os.FileMode = 0644

	PermissionDirectoryOwnerOnly os.FileMode = 0700
	PermissionFileOwnerOnly      os.FileMode = 0600
)
