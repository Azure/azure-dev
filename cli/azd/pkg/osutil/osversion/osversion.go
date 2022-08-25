//go:build !windows && !linux

package osversion

func GetVersion() string {
	return ""
}
