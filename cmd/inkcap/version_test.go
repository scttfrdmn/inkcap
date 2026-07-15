package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

// An -ldflags override must win over the embedded build info.
func TestVersionStringOverride(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v1.2.3"
	if got := versionString(); got != "v1.2.3" {
		t.Errorf("versionString() = %q, want %q", got, "v1.2.3")
	}
}

// With no override, versionString must still return something non-empty rather
// than the empty string (module version, VCS revision, or "(devel)").
func TestVersionStringFallbackNonEmpty(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = ""
	if got := versionString(); got == "" {
		t.Error("versionString() is empty with no override; want a fallback")
	}
}

func TestVCSRevisionShortAndDirty(t *testing.T) {
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	got := vcsRevision(bi)
	if !strings.HasSuffix(got, "+dirty") {
		t.Errorf("vcsRevision = %q, want a +dirty suffix", got)
	}
	if hash := strings.TrimSuffix(got, "+dirty"); len(hash) != 12 {
		t.Errorf("revision hash %q is %d chars, want 12", hash, len(hash))
	}
}

// No VCS revision recorded → empty, so callers fall through to "(devel)".
func TestVCSRevisionEmpty(t *testing.T) {
	if got := vcsRevision(&debug.BuildInfo{}); got != "" {
		t.Errorf("vcsRevision with no settings = %q, want empty", got)
	}
}
