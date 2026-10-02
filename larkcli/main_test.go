package larkcli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// shimPath is the one executable every fakeBinary links to. $0 is the link,
// so the script it runs, and every file the script writes beside itself, is
// the test's own.
var shimPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "larkcli-shim")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	shimPath = filepath.Join(dir, "shim")
	shim := "#!/bin/sh\nexec /bin/sh \"$(dirname \"$0\")/script\" \"$@\"\n"
	if err := os.WriteFile(shimPath, []byte(shim), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
