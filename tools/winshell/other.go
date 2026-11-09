//go:build !windows

// The Windows shell (main.go) only builds for Windows: GOOS=windows go build, or package.sh.
package main

import "fmt"

func main() {
	fmt.Println("tools/winshell is the Windows shell: build it with GOOS=windows (package.sh)")
}
