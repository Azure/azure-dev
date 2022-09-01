package osversion

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#import <Foundation/NSProcessInfo.h>

int toInt(NSNumber* i) {
	if (i == NULL) { return 0; }
    return i.intValue;
}

NSOperatingSystemVersion getVersion() {
	NSProcessInfo *pInfo = [NSProcessInfo processInfo];
	// check availability of the property operatingSystemVersion (10.10+) at runtime
    if ([pInfo respondsToSelector:@selector(operatingSystemVersion)])
    {
		return [pInfo operatingSystemVersion];
	}
	else
	{
		NSOperatingSystemVersion version;
		version.majorVersion = 10;
		version.minorVersion = 9;
		version.patchVersion = 0;
		return version;
	}
}
*/
import "C"

import (
	"errors"
	"fmt"
)

func verToStr(ver C.NSOperatingSystemVersion) string {
	major := C.toInt(ver.majorVersion)
	minor := C.toInt(ver.minorVersion)
	patch := C.toInt(ver.patchVersion)

	res := fmt.Sprintf("%d.%d.%d", major, minor, patch)
	log.Printf("MacOS version is %s\n", res)
	return res
}

func doGetVersion() {
	return verToStr(C.getVersion())
}

func GetVersion() (string, error) {
	return doGetVersion(), nil
}
