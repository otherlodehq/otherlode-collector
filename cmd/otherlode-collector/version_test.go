package main

import (
	"regexp"
	"testing"
)

func TestVersion_IsReleaseShaped(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-SNAPSHOT)?$`).MatchString(version) {
		t.Fatalf("version %q is not MAJOR.MINOR.PATCH with an optional -SNAPSHOT", version)
	}
}
