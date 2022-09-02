package osversion

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#import <Foundation/NSProcessInfo.h>

NSOperatingSystemVersion c_getVersion() {
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
	"fmt"
)

func verToStr(ver C.NSOperatingSystemVersion) string {
	res := fmt.Sprintf("%d.%d.%d", int(ver.majorVersion), int(ver.minorVersion), int(ver.patchVersion))
	return res
}

func doGetVersion() string {
	return verToStr(C.c_getVersion())
}

func GetVersion() (string, error) {
	return doGetVersion(), nil
}
