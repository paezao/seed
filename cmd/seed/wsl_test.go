package main

import (
	goruntime "runtime"
	"strings"
	"testing"
)

func TestWSL(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("WSL is Linux")
	}
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "")
	release := ""
	old := osRelease
	osRelease = func() string { return release }
	t.Cleanup(func() { osRelease = old })

	release = "6.8.0-45-generic"
	if inWSL() || checkSeedHome("/mnt/c/Users/me/app") != nil || engineHint() != "" {
		t.Fatal("plain Linux: no WSL rules")
	}
	if cmd, _ := browserCommand("http://localhost:8080/"); cmd != "xdg-open" {
		t.Fatalf("plain Linux opens with xdg-open, not %s", cmd)
	}

	release = "5.15.153.1-microsoft-standard-WSL2"
	if !inWSL() {
		t.Fatal("a Microsoft kernel is WSL")
	}
	for _, dir := range []string{"/mnt/c/Users/me/app", "/mnt/d", "/mnt/C/x"} {
		if err := checkSeedHome(dir); err == nil || !strings.Contains(err.Error(), "cd ~") {
			t.Errorf("%s: a Seed on a Windows drive is refused, with the way out: %v", dir, err)
		}
	}
	for _, dir := range []string{t.TempDir(), "/home/me/app", "/mnt/wsl/shared", "/mnt/cdrom"} {
		if err := checkSeedHome(dir); err != nil {
			t.Errorf("%s: Linux paths are fine: %v", dir, err)
		}
	}
	if cmd, args := browserCommand("http://localhost:8080/_seed/"); (cmd != "wslview" && cmd != "explorer.exe") || args[len(args)-1] != "http://localhost:8080/_seed/" {
		t.Fatalf("WSL opens the Windows browser: %s %v", cmd, args)
	}
	if !strings.Contains(engineHint(), "WSL integration") {
		t.Fatal("WSL: say where Docker's WSL integration is")
	}

	release = "6.8.0"
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	if !inWSL() {
		t.Fatal("WSL_DISTRO_NAME means WSL")
	}
}
