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
} OsVersion

OsVersion toOsVer(NSOperatingSystemVersion ver) {
	OsVersion v;
	v.major = toInt(ver.majorVersion);
	v.minor = toInt(ver.minorVersion);
	v.patch = toInt(ver.patchVersion);

	return v;
}

int toInt(NSNumber* i) {
	if (i == NULL) { return 0; }
    return i.intValue;
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
		NSOperatingSystemVersion version;
		version.majorVersion = [NSNumber numberWithInt: 10];
		version.minorVersion = [NSNumber numberWithInt: 9];
		version.patchVersion = [NSNumber numberWithInt: 0];
		return version;
	}
}
*/
import "C"

import (
	"fmt"
)

func verToStr(ver C.NSOperatingSystemVersion) string {
	major := C.toInt(ver.majorVersion)
	minor := C.toInt(ver.minorVersion)
	patch := C.toInt(ver.patchVersion)

	res := fmt.Sprintf("%d.%d.%d", major, minor, patch)
	return res
}

func doGetVersion() string {
	return verToStr(C.c_getVersion())
}

func GetVersion() (string, error) {
	return doGetVersion(), nil
}
