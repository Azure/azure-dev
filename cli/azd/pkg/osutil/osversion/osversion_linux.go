package osversion

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func GetVersion() (string, error) {
	uname := unix.Utsname{}
	err := unix.Uname(&uname)

	if err != nil {
		return "", fmt.Errorf("unix.Uname returned error: %w", err)
	}

	return string(uname.Release[:cStringLen(uname.Release[:])]), nil
}

func cStringLen(s []byte) int {
	for i := range s {
		if s[i] == 0 {
			return i
		}
	}

	return len(s)
}
