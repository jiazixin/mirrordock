package mirrors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPTReleaseCannotHideReferencesPastReadLimit(t *testing.T) {
	dir := t.TempDir()
	// A missing reference beyond the read limit must not be silently ignored.
	data := strings.Repeat("\n", 8<<20) + "SHA256:\n " + strings.Repeat("0", 64) + " 12 missing/Packages\n"
	file := filepath.Join(dir, "Release")
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Validate(t.Context(), dir, "rsync"); err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Fatal("truncated Release accepted", err)
	}
	if err := os.WriteFile(file, []byte("SHA256:\n "+strings.Repeat("0", 64)+" 12 missing/Packages\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Validate(t.Context(), dir, "rsync"); err == nil || !strings.Contains(err.Error(), "missing/Packages") {
		t.Fatal("missing reference accepted", err)
	}
}
