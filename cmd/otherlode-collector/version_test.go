package main

import (
	"regexp"
	"testing"
)

// TestVersion_IsReleaseShaped pins the form a release tag is checked
// against: the agent, the testkit and the collector share one number.
func TestVersion_IsReleaseShaped(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-SNAPSHOT)?$`).MatchString(version) {
		t.Fatalf("version %q is not MAJOR.MINOR.PATCH with an optional -SNAPSHOT", version)
	}
}
