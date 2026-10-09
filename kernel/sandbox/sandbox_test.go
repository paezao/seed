package sandbox

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func exercise(t *testing.T, d Driver, server string) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "kernel", "core.go"), []byte("package kernel"), 0o644)
	os.MkdirAll(filepath.Join(root, "organism"), 0o755)
	os.MkdirAll(filepath.Join(root, ".seed"), 0o755)
	os.WriteFile(filepath.Join(root, ".seed", "secret"), []byte("s3cret"), 0o644)

	sb, err := d.Create(ctx, Spec{
		Name: "seed-test-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")), Root: root,
		Writable: []string{"organism"}, Hidden: []string{".seed"}, Port: 8080,
		Env: map[string]string{"GREETING": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(ctx)

	res, err := sb.Exec(ctx, `echo "$GREETING" && echo out > organism/out.txt && exit 3`, 30*time.Second, nil)
	if err != nil || res.ExitCode != 3 || !strings.Contains(res.Output, "hello") {
		t.Fatalf("exec: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "organism", "out.txt")); string(b) != "out\n" {
		t.Fatalf("sandbox writes should reach the workspace, got %q", b)
	}

	res, _ = sb.Exec(ctx, `sleep 30`, 1*time.Second, nil)
	if !res.TimedOut {
		t.Fatalf("expected timeout, got %+v", res)
	}

	if d.Isolated() {
		res, _ = sb.Exec(ctx, `echo hacked > kernel/core.go`, 10*time.Second, nil)
		if res.OK() {
			t.Fatal("kernel path must be read-only inside the sandbox")
		}
		// New files outside the evolvable directories (go.work, vendor/) are refused too.
		res, _ = sb.Exec(ctx, `echo 'go 1.26' > go.work || mkdir vendor`, 10*time.Second, nil)
		if res.OK() {
			t.Fatal("the workspace root must be read-only inside the sandbox")
		}
		if b, _ := os.ReadFile(filepath.Join(root, "kernel", "core.go")); string(b) != "package kernel" {
			t.Fatal("kernel file was modified")
		}
		res, _ = sb.Exec(ctx, `cat .seed/secret`, 10*time.Second, nil)
		if res.OK() || strings.Contains(res.Output, "s3cret") {
			t.Fatalf("hidden path must be masked: %+v", res)
		}
		res, _ = sb.Exec(ctx, `id -u`, 10*time.Second, nil)
		if strings.TrimSpace(res.Output) == "0" {
			t.Fatal("sandbox must not run as root")
		}
	}

	if err := sb.Start(ctx, "app", server, nil); err != nil {
		t.Fatal(err)
	}
	url, err := sb.URL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var body string
	for i := 0; i < 50; i++ {
		resp, err := http.Get(url)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if body != "hi" {
		t.Fatalf("background server not reachable at %s (logs: %s)", url, sb.Logs(ctx, "app", 20))
	}
	if !sb.Running(ctx, "app") {
		t.Fatal("expected app running")
	}
	if err := sb.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if sb.Running(ctx, "app") {
		t.Fatal("expected app stopped")
	}
}

func TestLocalSandbox(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node needed for the test server")
	}
	exercise(t, LocalDriver{}, `exec node -e "require('http').createServer((q,r)=>r.end('hi')).listen(process.env.PORT)"`)
}

func TestBwrapSandbox(t *testing.T) {
	if !BwrapAvailable() {
		t.Skip("bubblewrap not available")
	}
	secretDir := t.TempDir()
	os.WriteFile(filepath.Join(secretDir, "key"), []byte("SECRET"), 0o600)
	d := &BwrapDriver{CacheDir: t.TempDir(), Hide: []string{secretDir}}
	exercise(t, d, `exec node -e "require('http').createServer((q,r)=>r.end('hi')).listen(process.env.PORT)"`)

	// Hidden host paths are invisible, and the sandbox gets a clean environment.
	ctx := context.Background()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "organism"), 0o755)
	t.Setenv("SEED_SECRET_ENV", "leak")
	sb, err := d.Create(ctx, Spec{Name: "hide", Root: root, Writable: []string{"organism"}})
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(ctx)
	res, _ := sb.Exec(ctx, "cat "+filepath.Join(secretDir, "key")+"; echo env=$SEED_SECRET_ENV; pwd", 10*time.Second, nil)
	if strings.Contains(res.Output, "SECRET") || strings.Contains(res.Output, "env=leak") || !strings.Contains(res.Output, "/workspace") {
		t.Fatalf("sandbox leaked host state: %q", res.Output)
	}
}

func TestBwrapPrivateNetwork(t *testing.T) {
	if !BwrapAvailable() {
		t.Skip("bubblewrap not available")
	}
	if _, err := exec.LookPath("socat"); err != nil {
		t.Skip("socat not available (run in the runtime image)")
	}
	ctx := context.Background()
	d := &BwrapDriver{CacheDir: t.TempDir()}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "organism"), 0o755)
	live, err := d.Create(ctx, Spec{Name: "live", Root: root, Writable: []string{"organism"}, Port: 8080, PrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(ctx)
	if err := live.Start(ctx, "app", `node -e "require('http').createServer((q,r)=>r.end('live')).listen(process.env.PORT)"`, nil); err != nil {
		t.Fatal(err)
	}
	url, _ := live.URL(ctx)
	client := &http.Client{Transport: live.Transport(), Timeout: 2 * time.Second}
	var body string
	for i := 0; i < 50 && body == ""; i++ {
		if resp, err := client.Get(url + "/"); err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
		} else {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if body != "live" {
		t.Fatalf("live process not reachable through its bridge (logs: %s)", live.Logs(ctx, "app", 20))
	}
	// Another sandbox (an evolution) cannot reach it over TCP, even on the port it listens on.
	other, err := d.Create(ctx, Spec{Name: "evo", Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(ctx)
	port := live.(*localSandbox).env["PORT"]
	res, _ := other.Exec(ctx, "curl -s -m 2 http://127.0.0.1:"+port+"/ || echo unreachable", 10*time.Second, nil)
	if strings.Contains(res.Output, "live") || !strings.Contains(res.Output, "unreachable") {
		t.Fatalf("the live process must be unreachable from other sandboxes: %q", res.Output)
	}
}

func TestPrivateProcessesDontGetTheSharedCache(t *testing.T) {
	d := &BwrapDriver{CacheDir: "/state/cache"}
	spec := Spec{Root: "/w", PrivateNetwork: true}
	build := strings.Join(d.args(spec, nil, nil, "go build", ""), " ")
	if !strings.Contains(build, "--bind /state/cache /cache") {
		t.Fatal("builds share the cache")
	}
	app := strings.Join(d.args(spec, nil, nil, "./server", "/tmp/bridge"), " ")
	if strings.Contains(app, "/state/cache") {
		t.Fatal("a private-network process (live organism, preview) must not get the shared cache")
	}
}
