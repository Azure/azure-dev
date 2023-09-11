// Command argprint prints each argument to standard out, separated by a newline.
package main

import (
	"fmt"
	"os"
)

func main() {
	for _, arg := range os.Args {
		fmt.Println(arg)
	}
}
