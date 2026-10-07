package runtime

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"seed/control"
	"seed/kernel/fsx"
	"seed/kernel/knowledge"
	"seed/kernel/memory"
	"seed/kernel/skills"
)

// Handler builds the kernel's HTTP surface: the control plane under /_seed
// and the live organism everywhere else.
func (k *Kernel) Handler() http.Handler {
	mux := http.NewServeMux()
	api := "/_seed/api"
	mux.HandleFunc("GET "+api+"/status", k.handleStatus)
	mux.HandleFunc("GET "+api+"/identity/logo", k.handleLogo)
	mux.HandleFunc("GET "+api+"/events", k.handleEvents)
	mux.HandleFunc("GET "+api+"/messages", k.handleMessages)
	mux.HandleFunc("POST "+api+"/messages", k.handlePostMessage)
	mux.HandleFunc("GET "+api+"/evolutions", k.handleEvolutions)
	mux.HandleFunc("POST "+api+"/evolutions", k.handleCreateEvolution)
	mux.HandleFunc("GET "+api+"/evolutions/{id}", k.handleEvolution)
	mux.HandleFunc("GET "+api+"/evolutions/{id}/diff", k.handleDiff)
	mux.HandleFunc("POST "+api+"/evolutions/{id}/cancel", k.handleCancel)
	mux.HandleFunc("POST "+api+"/evolutions/{id}/answer", k.handleAnswer)
	mux.HandleFunc("GET "+api+"/approvals", k.handleApprovals)
	mux.HandleFunc("POST "+api+"/approvals/{id}", k.handleDecide)
	mux.HandleFunc("GET "+api+"/generations", k.handleGenerations)
	mux.HandleFunc("POST "+api+"/generations/{n}/rollback", k.handleRollback)
	mux.HandleFunc("GET "+api+"/skills", k.handleSkills)
	mux.HandleFunc("GET "+api+"/skills/{name}", k.handleSkill)
	mux.HandleFunc("GET "+api+"/knowledge", k.handleKnowledge)
	mux.HandleFunc("GET "+api+"/knowledge/file", k.handleKnowledgeFile)
	mux.HandleFunc("GET "+api+"/logs", k.handleLogs)
	mux.HandleFunc("GET "+api+"/settings", k.handleSettings)
	mux.HandleFunc("GET "+api+"/extensions", k.handleExtensions)
	mux.HandleFunc("GET "+api+"/model", k.handleModel)
	mux.HandleFunc("GET "+api+"/model/options", k.handleModelOptions)
	mux.HandleFunc("POST "+api+"/model", k.handleSetModel)
	mux.HandleFunc("POST "+api+"/model/forget-key", k.handleForgetKey)
	mux.HandleFunc(api+"/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, errors.New("no such endpoint"))
	})
	mux.Handle("/_seed/", k.controlUI())
	mux.HandleFunc("/_seed", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/_seed/", http.StatusFound)
	})
	mux.Handle("/", k.Organism)
	return guardHost(k.Cfg.Server.AllowedHosts, organismOrigin(guardAPI(k.requireToken(mux))))
}

// OrganismFrameHost is the separate origin admin screens are framed from.
// Framed same-origin, an organism page could read window.parent and take
// the control token; on organism.localhost the browser walls it off.
const OrganismFrameHost = "organism.localhost"

// organismOrigin serves only the organism on the organism origin: never the
// control plane or its API.
func organismOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if strings.EqualFold(host, OrganismFrameHost) && (r.URL.Path == "/_seed" || strings.HasPrefix(r.URL.Path, "/_seed/")) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireToken protects the control-plane API with the kernel's token
// (header X-Seed-Token, or ?token= for EventSource). The logo is public.
func (k *Kernel) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_seed/api/") && r.URL.Path != "/_seed/api/identity/logo" {
			tok := r.Header.Get("X-Seed-Token")
			if tok == "" {
				tok = r.URL.Query().Get("token")
			}
			if subtle.ConstantTimeCompare([]byte(tok), []byte(k.Token)) != 1 {
				writeErr(w, http.StatusUnauthorized, errors.New("missing or invalid control token (reload the control plane)"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// guardHost refuses requests whose Host is not this machine (or explicitly
// allowed). Without it, a website could rebind its own domain to 127.0.0.1
// and become "same-origin" with the Seed.
func guardHost(extra []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, extra) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hostAllowed(hostport string, extra []string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	switch {
	case host == "localhost", host == "127.0.0.1", host == "::1", strings.HasSuffix(host, ".localhost"):
		return true
	}
	for _, e := range extra {
		if strings.EqualFold(e, host) || strings.EqualFold(e, hostport) {
			return true
		}
	}
	return false
}

// guardAPI requires state-changing control-plane requests to be JSON.
// Browsers cannot send application/json cross-origin without a CORS
// preflight (which the kernel never grants), so organism screens framed in
// a sandbox, or other sites, cannot drive the kernel, e.g. approve their own changes.
func guardAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_seed/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeErr(w, http.StatusUnsupportedMediaType, errors.New("control-plane requests must be application/json"))
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				writeErr(w, http.StatusForbidden, errors.New("cross-site request refused"))
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
					writeErr(w, http.StatusForbidden, errors.New("cross-origin request refused"))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// controlUI serves the embedded SPA, falling back to index.html for client routes.
func (k *Kernel) controlUI() http.Handler {
	ui := control.FS()
	files := http.FileServer(http.FS(ui))
	return http.StripPrefix("/_seed", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Nothing (including organism pages) may frame the control plane:
		// that would allow clickjacking the Approve button.
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p != "" {
			if st, err := fs.Stat(ui, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("cache-control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// The page carries the control token, so it is only served to real
		// top-level navigations: never to fetch()/XHR from organism scripts.
		if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
			http.Error(w, "the control plane can only be opened as a page", http.StatusForbidden)
			return
		}
		index, err := fs.ReadFile(ui, "index.html")
		if err != nil {
			http.Error(w, "control plane not built (run make control)", http.StatusInternalServerError)
			return
		}
		meta := `<meta name="seed-token" content="` + k.Token + `">`
		page := strings.Replace(string(index), "<head>", "<head>"+meta, 1)
		w.Header().Set("content-type", "text/html; charset=utf-8")
		w.Header().Set("cache-control", "no-store")
		// Isolate from organism windows (which the proxy forces to a different
		// opener policy), so a page cannot open /_seed and read its DOM.
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, _ = io.WriteString(w, page)
	}))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	if errors.Is(err, memory.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type identity struct {
	Name    string `json:"name"`
	Tagline string `json:"tagline"`
	Accent  string `json:"accent"`
	LogoURL string `json:"logo_url"`
}

type status struct {
	Name             string             `json:"name"`
	Purpose          string             `json:"purpose"`
	Identity         identity           `json:"identity"`
	Generation       *memory.Generation `json:"generation"`
	Organism         map[string]string  `json:"organism"`
	Model            any                `json:"model"`
	ActiveEvolution  *memory.Evolution  `json:"active_evolution"`
	PendingApprovals int                `json:"pending_approvals"`
}

// Status describes the Seed right now.
func (k *Kernel) Status(ctx context.Context) status {
	s := status{Name: k.Cfg.Name, Model: k.Mind.Info(), Identity: identity{Name: "Seed"}}
	if kn, err := knowledge.Load(k.Cfg.Root); err == nil {
		s.Purpose = kn.Purpose()
		if kn.Self != nil {
			id := kn.Self.Identity
			if id.Name != "" {
				s.Identity.Name = id.Name
			}
			s.Identity.Tagline, s.Identity.Accent = id.Tagline, id.Accent
		}
	}
	if b, err := fsx.ReadFile(k.Cfg.Root, knowledge.LogoPath); err == nil {
		sum := sha256.Sum256(b)
		s.Identity.LogoURL = "/_seed/api/identity/logo?v=" + hex.EncodeToString(sum[:6])
	}
	if g, err := k.Store.CurrentGeneration(ctx); err == nil {
		s.Generation = g
	}
	state, msg := k.Organism.State()
	s.Organism = map[string]string{"state": state}
	if msg != "" {
		s.Organism["error"] = msg
	}
	s.ActiveEvolution = k.Orch.Active(ctx)
	if aps, err := k.Store.PendingApprovals(ctx); err == nil {
		s.PendingApprovals = len(aps)
	}
	return s
}

func (k *Kernel) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, k.Status(r.Context()))
}

func (k *Kernel) handleLogo(w http.ResponseWriter, r *http.Request) {
	b, err := fsx.ReadFile(k.Cfg.Root, knowledge.LogoPath)
	if err != nil {
		writeErr(w, 404, errors.New("no logo"))
		return
	}
	w.Header().Set("content-type", "image/svg+xml")
	// The logo is generated content: never let it run script.
	w.Header().Set("content-security-policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("x-content-type-options", "nosniff")
	w.Header().Set("cache-control", "public, max-age=300")
	_, _ = w.Write(b)
}

func (k *Kernel) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("x-accel-buffering", "no")
	ch, cancel := k.Bus.Subscribe()
	defer cancel()
	send := func(typ string, data any) bool {
		b, err := json.Marshal(data)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	send("status", k.Status(r.Context()))
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Type == "organism" {
				// Organism state changes are delivered as a full status.
				if !send("status", k.Status(r.Context())) {
					return
				}
				continue
			}
			if !send(ev.Type, ev.Data) {
				return
			}
			if ev.Type == "evolution" || ev.Type == "approval" {
				send("status", k.Status(r.Context()))
			}
		}
	}
}

func (k *Kernel) handleMessages(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	msgs, err := k.Store.Messages(r.Context(), memory.DefaultConversation, limit)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if msgs == nil {
		msgs = []memory.Message{}
	}
	writeJSON(w, 200, msgs)
}

func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func (k *Kernel) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	var body struct{ Content string }
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	m, err := k.Chat.Post(r.Context(), body.Content)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 201, m)
}

func (k *Kernel) handleEvolutions(w http.ResponseWriter, r *http.Request) {
	evs, err := k.Store.Evolutions(r.Context(), 200)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if evs == nil {
		evs = []*memory.Evolution{}
	}
	writeJSON(w, 200, evs)
}

func (k *Kernel) handleCreateEvolution(w http.ResponseWriter, r *http.Request) {
	var body struct{ Intent string }
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	e, err := k.Orch.Request(r.Context(), memory.DefaultConversation, body.Intent)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if m, err := k.Store.AddMessage(r.Context(), memory.DefaultConversation, "user", body.Intent, e.ID); err == nil {
		k.Bus.Publish("message", m)
	}
	writeJSON(w, 201, e)
}

func (k *Kernel) handleEvolution(w http.ResponseWriter, r *http.Request) {
	e, err := k.Store.Evolution(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	evs, err := k.Store.Events(r.Context(), e.ID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if evs == nil {
		evs = []memory.Event{}
	}
	writeJSON(w, 200, map[string]any{"evolution": e, "events": evs})
}

// handleDiff shows an evolution's changes: from its commit if it has one,
// otherwise from its (in-progress or preserved) worktree.
func (k *Kernel) handleDiff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	e, err := k.Store.Evolution(ctx, r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	var stat, diff string
	switch {
	case e.Commit != "" && e.BaseCommit != "":
		stat, _ = k.Repo.Diff(ctx, "--stat", e.BaseCommit, e.Commit)
		diff, _ = k.Repo.Diff(ctx, e.BaseCommit, e.Commit)
	case e.Worktree != "":
		if _, err := os.Stat(e.Worktree); err == nil {
			wt := k.Repo.WithDir(e.Worktree)
			stat, _ = wt.DiffWorking(ctx, true)
			diff, _ = wt.DiffWorking(ctx, false)
		}
	}
	if len(diff) > 2<<20 {
		diff = diff[:2<<20] + "\n… (diff truncated)"
	}
	writeJSON(w, 200, map[string]string{"stat": stat, "diff": diff})
}

func (k *Kernel) handleCancel(w http.ResponseWriter, r *http.Request) {
	e, err := k.Orch.Cancel(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, e)
}

func (k *Kernel) handleApprovals(w http.ResponseWriter, r *http.Request) {
	aps, err := k.Store.PendingApprovals(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if aps == nil {
		aps = []memory.Approval{}
	}
	writeJSON(w, 200, aps)
}

func (k *Kernel) handleDecide(w http.ResponseWriter, r *http.Request) {
	var body struct{ Approved bool }
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	ap, err := k.Approvals.Decide(r.Context(), r.PathValue("id"), body.Approved)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, ap)
}

func (k *Kernel) handleGenerations(w http.ResponseWriter, r *http.Request) {
	gens, err := k.Store.Generations(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if gens == nil {
		gens = []memory.Generation{}
	}
	writeJSON(w, 200, gens)
}

func (k *Kernel) handleRollback(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	e, err := k.Orch.RequestRollback(r.Context(), memory.DefaultConversation, n)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if m, err := k.Store.AddMessage(r.Context(), memory.DefaultConversation, "user", e.Intent, e.ID); err == nil {
		k.Bus.Publish("message", m)
	}
	writeJSON(w, 201, e)
}

func (k *Kernel) handleSkills(w http.ResponseWriter, r *http.Request) {
	list := skills.Library{Root: k.Cfg.Root}.List()
	if list == nil {
		list = []skills.Skill{}
	}
	writeJSON(w, 200, list)
}

func (k *Kernel) handleSkill(w http.ResponseWriter, r *http.Request) {
	s, err := skills.Library{Root: k.Cfg.Root}.Get(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, s)
}

func (k *Kernel) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	kn, err := knowledge.Load(k.Cfg.Root)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if kn.Docs == nil {
		kn.Docs = []knowledge.Doc{}
	}
	if kn.Decisions == nil {
		kn.Decisions = []knowledge.Doc{}
	}
	writeJSON(w, 200, kn)
}

func (k *Kernel) handleKnowledgeFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	content, err := knowledge.ReadFile(k.Cfg.Root, p)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]string{"path": p, "content": content})
}

func (k *Kernel) handleLogs(w http.ResponseWriter, r *http.Request) {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 || tail > 5000 {
		tail = 500
	}
	var lines []string
	switch r.URL.Query().Get("source") {
	case "kernel":
		lines = k.Logs.Tail(tail)
	default:
		out := k.Organism.Logs(r.Context(), tail)
		if out != "" {
			lines = strings.Split(out, "\n")
		}
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, 200, map[string][]string{"lines": lines})
}

func (k *Kernel) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"config":  k.Cfg,
		"model":   k.Mind.Info(),
		"sandbox": map[string]any{"driver": k.Cfg.Sandbox.Driver, "isolated": k.Driver.Isolated(), "image": k.SandboxImage},
	})
}

// Extensions are control-plane pages contributed by the organism, declared
// in organism/control/extensions.json as [{id, title, path}]. The control
// plane renders each as a page that frames the organism's path.
func (k *Kernel) handleExtensions(w http.ResponseWriter, r *http.Request) {
	type ext struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Path  string `json:"path"`
	}
	out := []ext{}
	b, err := fsx.ReadFile(k.Cfg.Root, "organism/control/extensions.json")
	if err == nil {
		var list []ext
		if json.Unmarshal(b, &list) == nil {
			for _, e := range list {
				if e.ID != "" && e.Title != "" && strings.HasPrefix(e.Path, "/") && !strings.HasPrefix(e.Path, "/_seed") {
					out = append(out, e)
				}
			}
		}
	}
	writeJSON(w, 200, out)
}

// handleAnswer answers the questions an evolution is waiting on (e.g. by
// picking a suggested option on its card).
func (k *Kernel) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct{ Answer string }
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	id := r.PathValue("id")
	if k.Orch.WaitingForAnswer() != id {
		writeErr(w, 409, errors.New("that evolution is not waiting for an answer"))
		return
	}
	m, err := k.Store.AddMessage(r.Context(), memory.DefaultConversation, "user", strings.TrimSpace(body.Answer), id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	k.Bus.Publish("message", m)
	if err := k.Orch.Answer(r.Context(), id, body.Answer); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, m)
}
