package version

import "testing"

func TestStringNonEmpty(t *testing.T) {
	t.Parallel()
	if String() == "" {
		t.Fatal("String() returned empty")
	}
}

func TestDefaultVersion(t *testing.T) {
	t.Parallel()
	if Version == "" {
		t.Fatal("Version is empty by default; release builds rely on a non-empty default")
	}
}
