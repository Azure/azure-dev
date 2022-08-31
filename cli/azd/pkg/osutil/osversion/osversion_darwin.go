package osversion

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#import <Foundation/NSProcessInfo.h>

int toInt(NSNumber i) {
    return i.intValue;
}

NSOperatingSystemVersion getVersion() {
	NSProcessInfo *pinfo = [NSProcessInfo processInfo];
	// check availability of the property operatingSystemVersion (10.10+) at runtime
    if ([processInfo respondsToSelector:@selector(operatingSystemVersion)])
    {
		return [pInfo operatingSystemVersion];
	}
	else
	{
		struct Result NSOperatingSystemVersion;
		Result.majorVersion = 10;
		Result.minorVersion = 9;
		Result.patchVersion = 0;
		return Result;
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
