// Package shortcut puts magpie in the Start menu on Windows. There it is
// one portable exe with no installer, so nothing else would: each start of
// the desktop app points a Magpie shortcut in the user's Start menu at the
// exe it runs as, and Windows search finds it by that. Elsewhere it does
// nothing — the Mac has the app in Applications, Linux its own ways.
package shortcut

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Name is what the shortcut is called, and what Windows search finds.
const Name = "Magpie"

// Ensure has the shortcut point at this magpie, when it doesn't yet. It
// fails quietly — magpie runs the same without one — and is skipped with
// MAGPIE_NO_SHORTCUT=1 (tests, sandboxes).
func Ensure() {
	if os.Getenv("MAGPIE_NO_SHORTCUT") == "1" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	if err := ensure(Target(exe)); err != nil {
		log.Println("start menu shortcut:", err)
	}
}

// aside is what an update moves the exe it replaces to, or stages the new
// one as: magpie.exe.old, magpie.exe.old-2…, magpie.exe.new
var aside = regexp.MustCompile(`(?i)(\.exe)\.(old(-\d+)?|new)$`)

// Target is what the shortcut opens for exe: exe itself, but for a magpie
// left running from where an update moved it aside, the exe in its place,
// which is what the next start runs. An update replaces the exe where it
// is, so the shortcut stays good through it.
func Target(exe string) string {
	return aside.ReplaceAllString(exe, "$1")
}

// Stale reports whether a shortcut opening target in dir is not one for
// exe: there is none yet, or the exe was moved. Windows paths are the same
// in any case.
func Stale(target, dir, exe string) bool {
	same := func(a, b string) bool {
		return a != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return !same(target, exe) || !same(dir, filepath.Dir(exe))
}
