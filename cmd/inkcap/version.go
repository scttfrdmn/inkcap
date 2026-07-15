package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// version is overridable at build time with:
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/inkcap
//
// When left at its default, versionString falls back to the module version
// recorded by the Go toolchain, so `go install ...@v0.1.0` also reports a
// meaningful version.
var version = ""

// versionString reports the build version, preferring an -ldflags override,
// then the VCS/module info embedded by `go install`, then "(devel)".
func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
		if rev := vcsRevision(bi); rev != "" {
			return rev
		}
	}
	return "(devel)"
}

// vcsRevision extracts a short commit hash (with a +dirty suffix) from the
// build settings, for binaries built from a checkout rather than a tag.
func vcsRevision(bi *debug.BuildInfo) string {
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}

// printVersion writes the version line, including Go version and platform.
func printVersion() {
	fmt.Printf("inkcap %s %s %s/%s\n",
		versionString(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
