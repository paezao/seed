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
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed for the test server")
	}
	exercise(t, LocalDriver{}, `exec python3 -c "
import http.server,os
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(s):
        s.send_response(200); s.end_headers(); s.wfile.write(b'hi')
http.server.HTTPServer(('127.0.0.1', int(os.environ['PORT'])), H).serve_forever()"`)
}

func TestDockerSandbox(t *testing.T) {
	image := os.Getenv("SEED_TEST_SANDBOX_IMAGE")
	if image == "" {
		t.Skip("set SEED_TEST_SANDBOX_IMAGE to run Docker sandbox tests")
	}
	d := &DockerDriver{Image: image, CacheDir: t.TempDir(), Memory: "512m", CPUs: "1"}
	exercise(t, d, `exec node -e "require('http').createServer((q,r)=>r.end('hi')).listen(process.env.PORT)"`)
}
