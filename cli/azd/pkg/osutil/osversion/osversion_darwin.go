package osversion

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#import <Foundation/NSProcessInfo.h>

typedef struct _OsVersion {
	int major;
	int minor;
	int patch;
} OsVersion;

int toInt(NSNumber* i) {
	if (i == NULL) { return 0; }
	return i.intValue;
}

OsVersion toOsVer(NSOperatingSystemVersion ver) {
	OsVersion v;
	v.major = toInt(ver.majorVersion);
	v.minor = toInt(ver.minorVersion);
	v.patch = toInt(ver.patchVersion);

	return v;
}
OsVersion c_getVersion() {
	NSProcessInfo *pInfo = [NSProcessInfo processInfo];
	// check availability of the property operatingSystemVersion (10.10+) at runtime
	if ([pInfo respondsToSelector:@selector(operatingSystemVersion)])
	{
		return toOsVer([pInfo operatingSystemVersion]);
	}
	else
	{
		OsVersion version;
		version.major = 10;
		version.minor = 9;
		version.patch = 0;
		return version;
	}
}
*/
import "C"

import (
	"fmt"
)

func verToStr(ver C.OsVersion) string {
	res := fmt.Sprintf("%d.%d.%d", int(ver.major), int(ver.minor), int(ver.patch))
	return res
}

func doGetVersion() string {
	return verToStr(C.c_getVersion())
}

func GetVersion() (string, error) {
	return doGetVersion(), nil
}
