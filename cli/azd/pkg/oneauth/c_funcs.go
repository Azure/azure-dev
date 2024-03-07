// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build oneauth

package oneauth

/*
extern void goLog(char *s);

// enables native code to call goLog (can't pass a Go function pointer to C)
void goLogGateway(char *s) {
	goLog(s);
}
*/
import "C"
