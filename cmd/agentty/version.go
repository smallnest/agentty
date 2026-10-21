package main

import (
	"fmt"
	"runtime"
)

// These values are injected by Makefile and GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func printVersion() {
	fmt.Printf("agentty %s\n", version)
	fmt.Printf("  commit:   %s\n", commit)
	fmt.Printf("  built:    %s\n", date)
	fmt.Printf("  go:       %s\n", runtime.Version())
	fmt.Printf("  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
}
