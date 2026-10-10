package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
)

// Windows runs seed inside WSL2 (with Docker Desktop's WSL integration).
// What differs there: the browser is Windows', and a Seed must live in the
// Linux filesystem, not on a Windows drive.

// osRelease is the kernel's release string (tests replace it).
var osRelease = func() string {
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return string(b)
}

// inWSL reports whether this runs inside Windows Subsystem for Linux.
func inWSL() bool {
	if goruntime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	return strings.Contains(strings.ToLower(osRelease()), "microsoft")
}

var windowsDrive = regexp.MustCompile(`^/mnt/[a-zA-Z](/|$)`)

// checkSeedHome refuses a Seed folder on a Windows drive under WSL: its
// database needs Linux file permissions, which Windows drives don't keep,
// and they are slow from WSL.
func checkSeedHome(dir string) error {
	if !inWSL() {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if !windowsDrive.MatchString(abs) {
		return nil
	}
	return fmt.Errorf("%s is on a Windows drive. A Seed's database needs Linux file permissions, which Windows drives don't keep (and they're slow from WSL). Plant it in your Linux home instead:\n\n  cd ~ && seed new <name>", abs)
}

// browserCommand opens a URL: in WSL, in the Windows browser.
func browserCommand(link string) (string, []string) {
	switch {
	case goruntime.GOOS == "darwin":
		return "open", []string{link}
	case goruntime.GOOS == "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", link}
	case inWSL():
		if _, err := exec.LookPath("wslview"); err == nil {
			return "wslview", []string{link}
		}
		return "explorer.exe", []string{link}
	default:
		return "xdg-open", []string{link}
	}
}

// engineHint adds what usually fixes an unreachable container engine.
func engineHint() string {
	if inWSL() {
		return "\nIn WSL: start Docker Desktop on Windows and turn on its WSL integration for this distro (Settings → Resources → WSL integration)."
	}
	return ""
}
