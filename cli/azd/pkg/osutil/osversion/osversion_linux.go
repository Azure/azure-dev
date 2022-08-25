package osversion

import "golang.org/x/sys/unix"

func GetVersion() (string, error) {
	uname := unix.Utsname{}
	err := unix.Uname(&uname)

	if err != nil {
		return string(uname.Release[:cStringLen(uname.Release)]), nil
	}

	return "", fmt.Errorf("unix.Uname returned error: %s", err)
}

func cStringLen(s []byte) []byte {
	for i := range s {
		if s[i] == 0 {
			return i
		}
	}

	return len(s)
}
