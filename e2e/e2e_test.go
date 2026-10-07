// Package e2e runs the canonical Seed demonstration against a real model:
//
//	seed new tasks → seed run → "become a todo application" → generation 2
//	              → "add priorities and filtering"          → generation 3
//
// Nothing about todos is hard-coded in Seed. This test only checks generic
// properties: generations, health, identity, self model, git history.
//
// Run with `make e2e` (needs Docker, PostgreSQL via `make up`, and a model API
// key in the environment). It takes 5-15 minutes and costs real tokens.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var token string

type status struct {
	Model struct {
		Configured bool `json:"configured"`
	} `json:"model"`
	Purpose  string `json:"purpose"`
	Identity struct {
		Name string `json:"name"`
	} `json:"identity"`
	Generation *struct {
		Number int    `json:"number"`
		Commit string `json:"commit"`
	} `json:"generation"`
	Organism map[string]string `json:"organism"`
}

type evolution struct {
	Questions []struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	} `json:"questions"`
	ID            string `json:"id"`
	Intent        string `json:"intent"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	NewGeneration *int   `json:"new_generation"`
}

func TestCanonicalDemo(t *testing.T) {
	if os.Getenv("SEED_E2E") == "" {
		t.Skip("set SEED_E2E=1 to run the end-to-end demo (uses a real model)")
	}
	// A dedicated test key keeps test spend separate from the owner's own.
	key := os.Getenv("SEED_TEST_OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("SEED_TEST_OPENROUTER_API_KEY is needed: the test gives the Seed its mind like an owner would")
	}
	model := os.Getenv("SEED_E2E_MODEL")
	if model == "" {
		model = "anthropic/claude-sonnet-5.5"
	}
	bin, err := filepath.Abs("../bin/seed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatal("build first: make build")
	}
	dir := t.TempDir()
	name := fmt.Sprintf("e2e%d", time.Now().Unix()%100000)
	run(t, dir, bin, "new", "--no-run", name)
	root := filepath.Join(dir, name)

	port := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Started the way an owner starts a Seed: the key passed in at start, never stored.
	cmd := exec.CommandContext(ctx, bin, "run", "--addr", fmt.Sprintf("127.0.0.1:%d", port), "-e", "OPENROUTER_API_KEY", "-e", "SEED_MODEL")
	cmd.Dir = root
	// The Seed runs in its container with its own private PostgreSQL.
	cmd.Env = append(os.Environ(), "OPENROUTER_API_KEY="+key, "SEED_MODEL="+model)
	logf, _ := os.Create(filepath.Join(dir, "seed.log"))
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(dir, "seed.log"))
			t.Logf("seed log tail:\n%s", tail(string(b), 4000))
		}
	}()

	// The fresh Seed has no purpose, no mind, and a running (empty) organism.
	var s status
	waitFor(t, 5*time.Minute, func() bool {
		b, err := os.ReadFile(filepath.Join(root, ".seed", "control-token"))
		token = strings.TrimSpace(string(b))
		return err == nil && getJSON(base+"/_seed/api/status", &s) == nil && s.Organism["state"] == "running"
	})
	if !s.Model.Configured {
		t.Fatal("a Seed started with OPENROUTER_API_KEY should be able to think right away")
	}
	if s.Purpose != "" || s.Generation.Number != 1 || s.Identity.Name != "Seed" {
		t.Fatalf("fresh seed should have no purpose at generation 1: %+v", s)
	}

	evolve := func(message string, wantGen int) {
		t.Helper()
		known := map[string]bool{}
		var before []evolution
		_ = getJSON(base+"/_seed/api/evolutions", &before)
		for _, e := range before {
			known[e.ID] = true
		}
		post(t, base+"/_seed/api/messages", map[string]string{"content": message})
		var evo evolution
		// The Seed's chat agent decides to start an evolution.
		defer func() {
			if t.Failed() {
				var msgs []struct{ Role, Content string }
				_ = getJSON(base+"/_seed/api/messages?limit=6", &msgs)
				for _, m := range msgs {
					t.Logf("chat %s: %s", m.Role, tail(m.Content, 600))
				}
			}
		}()
		waitFor(t, 3*time.Minute, func() bool {
			var evs []evolution
			if getJSON(base+"/_seed/api/evolutions", &evs) != nil {
				return false
			}
			for _, e := range evs {
				if !known[e.ID] {
					evo = e
					return true
				}
			}
			return false
		})
		t.Logf("evolution %s: %s", evo.ID, evo.Intent)
		waitFor(t, 45*time.Minute, func() bool {
			var d struct{ Evolution evolution }
			if getJSON(base+"/_seed/api/evolutions/"+evo.ID, &d) != nil {
				return false
			}
			evo = d.Evolution
			// The Seed may ask its owner; answer like an owner who takes its recommendations.
			if evo.Status == "needs_input" && len(evo.Questions) > 0 {
				var parts []string
				for i, q := range evo.Questions {
					choice := "go with your recommendation"
					if len(q.Options) > 0 {
						choice = q.Options[0]
					}
					parts = append(parts, fmt.Sprintf("%d. %s", i+1, choice))
				}
				t.Logf("the Seed asked %d question(s); answering with its recommendations", len(evo.Questions))
				post(t, base+"/_seed/api/evolutions/"+evo.ID+"/answer", map[string]string{"answer": strings.Join(parts, "\n")})
				return false
			}
			switch evo.Status {
			case "complete":
				return true
			case "failed", "cancelled", "rolled_back":
				t.Fatalf("evolution %s ended %s: %s", evo.ID, evo.Status, evo.Error)
			}
			return false
		})
		if *evo.NewGeneration != wantGen {
			t.Fatalf("expected generation %d, got %d", wantGen, *evo.NewGeneration)
		}
		waitFor(t, 2*time.Minute, func() bool {
			return getJSON(base+"/_seed/api/status", &s) == nil && s.Organism["state"] == "running" && s.Generation.Number == wantGen
		})
		resp, err := http.Get(base + "/")
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("GET / after generation %d: %v %v", wantGen, err, resp)
		}
		resp.Body.Close()
	}

	evolve("Become a todo application. I need to create, complete and delete tasks.", 2)
	if s.Purpose == "" || s.Identity.Name == "Seed" {
		t.Fatalf("after becoming something the Seed should know its purpose and have a new identity: %+v", s)
	}
	t.Logf("generation 2: %s — %s", s.Identity.Name, s.Purpose)

	evolve("Tasks should have priorities: low, medium and high. Let me filter by priority.", 3)
	self, _ := os.ReadFile(filepath.Join(root, "knowledge", "self.yaml"))
	if !strings.Contains(strings.ToLower(string(self)), "priorit") {
		t.Fatalf("self model should know about priorities:\n%s", self)
	}

	// Git is the evolutionary record: three generations with evolution trailers.
	out := run(t, root, "git", "log", "--format=%s%n%b")
	if strings.Count(out, "Generation: ") != 3 || strings.Count(out, "Evolution: evo_") != 2 {
		t.Fatalf("unexpected history:\n%s", out)
	}
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("timed out after %s", d)
}

func getJSON(url string, v any) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Seed-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func post(t *testing.T, url string, body any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Seed-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: %d %s", url, resp.StatusCode, msg)
	}
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
