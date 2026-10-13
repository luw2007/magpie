package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

// magpie daemon: status before an install says how to install; start
// says to install first; a word it doesn't take prints its usage; and a
// `go test` or `go run` build, which Go deletes, is never installed as the
// service. None of them runs the system's service tool.
func TestDaemonCmd(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("systemd and launchd only")
	}
	helpHome(t)
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	for _, name := range []string{"systemctl", "launchctl", "loginctl"} {
		testenv.Program(t, filepath.Join(dir, name), "#!/bin/sh\necho \""+name+" $*\" >> '"+calls+"'\n")
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	prev := daemonWait
	daemonWait = 0
	t.Cleanup(func() { daemonWait = prev })
	out, err := runOut(t, "daemon")
	if err != nil || !strings.Contains(out, "isn't installed") || !strings.Contains(out, "magpie daemon install") {
		t.Errorf("status: %q, %v", out, err)
	}
	if _, err := runOut(t, "daemon", "start"); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Errorf("start: %v", err)
	}
	if _, err := runOut(t, "daemon", "bogus"); err == nil || !strings.Contains(err.Error(), "usage: magpie daemon") {
		t.Errorf("bogus: %v", err)
	}
	if _, err := runOut(t, "daemon", "install"); err == nil || !strings.Contains(err.Error(), "go run") {
		t.Errorf("install from a test build: %v", err)
	}
	if b, _ := os.ReadFile(calls); len(b) > 0 {
		t.Errorf("ran %s", b)
	}
}
