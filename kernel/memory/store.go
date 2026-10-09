// Package memory is the Seed's operational memory in PostgreSQL:
// conversations, evolutions and their event logs, generations and approvals.
//
// Durable *semantic* knowledge lives in the repository (knowledge/, skills/);
// this package records what happened, so an interrupted Seed can understand
// its own recent past after a restart.
package memory

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"seed/kernel/ids"
	"seed/kernel/migrate"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const DefaultConversation = "default"

type Message struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Role           string `json:"role"`
	Content        string `json:"content"`
	EvolutionID    string `json:"evolution_id,omitempty"`
	// Images are ids of images my owner attached (see AddImage).
	Images []string `json:"images,omitempty"`
	// Kind is "chat" (a conversational turn) or "report" (written by the
	// kernel about an evolution, in the Seed's voice).
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
}

type Plan struct {
	Title        string     `json:"title"`
	Scope        string     `json:"scope"`
	Summary      string     `json:"summary"`
	Steps        []PlanStep `json:"steps"`
	Capabilities []string   `json:"capabilities"`
	Risks        []string   `json:"risks"`
	// Staging: when the owner's goal is bigger than one evolution, Goal is the
	// whole goal, Stages the full ordered roadmap, and Stage the 1-based stage
	// this evolution builds. Empty for single-step evolutions.
	Goal   string  `json:"goal,omitempty"`
	Stages []Stage `json:"stages,omitempty"`
	Stage  int     `json:"stage,omitempty"`
}

// Preview is how trying an evolution before it goes live is going.
type Preview struct {
	// Skip: this evolution goes live without waiting (e.g. a fix the Seed
	// started on its own, or previews turned off).
	Skip bool `json:"skip,omitempty"`
	// State: starting, ready (waiting for the owner), failed (couldn't
	// start: the owner can still apply or discard), or done.
	State string `json:"state,omitempty"`
	// Data: "copy" (a copy of the live data) or "fresh" (only migrations).
	Data string `json:"data,omitempty"`
	Note string `json:"note,omitempty"`
	// Round counts changes the owner asked for after previewing.
	Round int `json:"round,omitempty"`
}

// Stage is one step of a larger goal.
type Stage struct {
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

// Question is something the Seed asks its owner during an evolution.
type Question struct {
	Question string   `json:"question"`
	Why      string   `json:"why,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// Clarification is one round of questions and the owner's answer.
type Clarification struct {
	Questions []Question `json:"questions"`
	Answer    string     `json:"answer"`
}

type PlanStep struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

type Check struct {
	Name        string `json:"name"`
	OK          bool   `json:"ok"`
	Detail      string `json:"detail"`
	Attempt     int    `json:"attempt"`
	TestsPassed *int   `json:"tests_passed,omitempty"`
	TestsFailed *int   `json:"tests_failed,omitempty"`
}

type Reflection struct {
	Summary       string `json:"summary"`
	GoalSatisfied bool   `json:"goal_satisfied"`
	// Gaps say what was not satisfied and why (required when GoalSatisfied is false).
	Gaps      []string `json:"gaps,omitempty"`
	Decisions []string `json:"decisions"`
	Learnings []string `json:"learnings"`
	Skills    []string `json:"skills"`
	Debt      []string `json:"debt"`
}

type Evolution struct {
	ID               string      `json:"id"`
	ConversationID   string      `json:"conversation_id,omitempty"`
	Kind             string      `json:"kind"`
	Intent           string      `json:"intent"`
	Title            string      `json:"title"`
	Status           Status      `json:"status"`
	Plan             *Plan       `json:"plan"`
	BaseGeneration   int         `json:"base_generation"`
	NewGeneration    *int        `json:"new_generation,omitempty"`
	TargetGeneration *int        `json:"target_generation,omitempty"`
	BaseCommit       string      `json:"base_commit,omitempty"`
	Branch           string      `json:"branch"`
	Worktree         string      `json:"-"`
	Commit           string      `json:"commit,omitempty"`
	Attempts         int         `json:"attempts"`
	Checks           []Check     `json:"checks"`
	Reflection       *Reflection `json:"reflection"`
	Summary          string      `json:"summary,omitempty"`
	Error            string      `json:"error,omitempty"`
	Usage            Usage       `json:"usage"`
	// Questions the evolution is waiting on (needs_input), if any.
	Questions      []Question      `json:"questions,omitempty"`
	Clarifications []Clarification `json:"clarifications,omitempty"`
	// Preview: trying the new generation before it goes live.
	Preview *Preview `json:"preview,omitempty"`
	// Images my owner attached to the request (see AddImage).
	Images      []string   `json:"images,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type Usage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
	ModelCalls      int `json:"model_calls"`
}

type Event struct {
	ID          int64     `json:"id"`
	EvolutionID string    `json:"evolution_id"`
	Kind        string    `json:"kind"`
	Summary     string    `json:"summary"`
	Data        any       `json:"data"`
	CreatedAt   time.Time `json:"created_at"`
}

type Generation struct {
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	Intent       string    `json:"intent"`
	EvolutionID  string    `json:"evolution_id,omitempty"`
	Commit       string    `json:"commit"`
	ParentCommit string    `json:"parent_commit,omitempty"`
	Current      bool      `json:"current"`
	TestsPassed  *int      `json:"tests_passed,omitempty"`
	BuildOK      *bool     `json:"build_ok,omitempty"`
	HealthOK     *bool     `json:"health_ok,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Approval struct {
	ID          string     `json:"id"`
	EvolutionID string     `json:"evolution_id,omitempty"`
	Action      string     `json:"action"`
	Level       string     `json:"level"`
	Detail      string     `json:"detail"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
}

var ErrNotFound = errors.New("not found")

type Store struct {
	Pool *pgxpool.Pool
}

// Open connects and applies the kernel's own migrations.
func Open(ctx context.Context, url string) (*Store, error) {
	migs, err := migrate.Load(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, err
	}
	if _, err := migrate.Apply(ctx, conn, "seed_kernel_migrations", migs); err != nil {
		conn.Close(ctx)
		return nil, err
	}
	conn.Close(ctx)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	s := &Store{Pool: pool}
	_, err = pool.Exec(ctx, `INSERT INTO conversations (id, title) VALUES ($1, 'Owner') ON CONFLICT DO NOTHING`, DefaultConversation)
	return s, err
}

func (s *Store) Close() { s.Pool.Close() }

// ---- messages

func (s *Store) AddMessage(ctx context.Context, conv, role, content, evolutionID string) (*Message, error) {
	return s.addMessage(ctx, conv, role, "chat", content, evolutionID)
}

// AddOwnerMessage records a message from my owner, with any images they attached.
func (s *Store) AddOwnerMessage(ctx context.Context, conv, content, evolutionID string, images []string) (*Message, error) {
	m, err := s.addMessage(ctx, conv, "user", "chat", content, evolutionID)
	if err != nil || len(images) == 0 {
		return m, err
	}
	m.Images = images
	_, err = s.Pool.Exec(ctx, `UPDATE messages SET images=$2 WHERE id=$1`, m.ID, images)
	return m, err
}

// ---- images

// AddImage stores an image my owner attached; it returns its id.
func (s *Store) AddImage(ctx context.Context, mediaType string, data []byte) (string, error) {
	id := ids.New("img")
	_, err := s.Pool.Exec(ctx, `INSERT INTO images (id, media_type, data) VALUES ($1, $2, $3)`, id, mediaType, data)
	return id, err
}

// Image returns a stored image and its media type.
func (s *Store) Image(ctx context.Context, id string) (string, []byte, error) {
	var mt string
	var data []byte
	err := s.Pool.QueryRow(ctx, `SELECT media_type, data FROM images WHERE id=$1`, id).Scan(&mt, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	return mt, data, err
}

// AddReport records a kernel-written report about an evolution.
func (s *Store) AddReport(ctx context.Context, conv, content, evolutionID string) (*Message, error) {
	return s.addMessage(ctx, conv, "seed", "report", content, evolutionID)
}

func (s *Store) addMessage(ctx context.Context, conv, role, kind, content, evolutionID string) (*Message, error) {
	m := &Message{ID: ids.New("msg"), ConversationID: conv, Role: role, Kind: kind, Content: content, EvolutionID: evolutionID}
	err := s.Pool.QueryRow(ctx, `INSERT INTO messages (id, conversation_id, role, kind, content, evolution_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')) RETURNING created_at`, m.ID, conv, role, kind, content, evolutionID).Scan(&m.CreatedAt)
	return m, err
}

// Messages returns the last limit messages, oldest first.
func (s *Store) Messages(ctx context.Context, conv string, limit int) ([]Message, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, conversation_id, role, kind, content, coalesce(evolution_id, ''), images, created_at FROM (
		SELECT * FROM messages WHERE conversation_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2) m
		ORDER BY created_at, id`, conv, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) {
		var m Message
		err := r.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Kind, &m.Content, &m.EvolutionID, &m.Images, &m.CreatedAt)
		if len(m.Images) == 0 {
			m.Images = nil
		}
		return m, err
	})
}

// ---- evolutions

const evoCols = `id, coalesce(conversation_id, ''), kind, intent, title, status, plan, base_generation, new_generation,
	target_generation, base_commit, branch, worktree, commit, attempts, checks, reflection, summary, error, usage,
	created_at, updated_at, completed_at, questions, clarifications, preview, images`

func scanEvolution(r pgx.Row) (*Evolution, error) {
	var e Evolution
	var plan, checks, refl, usage, questions, clar, preview []byte
	err := r.Scan(&e.ID, &e.ConversationID, &e.Kind, &e.Intent, &e.Title, &e.Status, &plan, &e.BaseGeneration,
		&e.NewGeneration, &e.TargetGeneration, &e.BaseCommit, &e.Branch, &e.Worktree, &e.Commit, &e.Attempts,
		&checks, &refl, &e.Summary, &e.Error, &usage, &e.CreatedAt, &e.UpdatedAt, &e.CompletedAt, &questions, &clar, &preview, &e.Images)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(plan) > 0 && string(plan) != "null" {
		e.Plan = &Plan{}
		_ = json.Unmarshal(plan, e.Plan)
	}
	_ = json.Unmarshal(checks, &e.Checks)
	if e.Checks == nil {
		e.Checks = []Check{}
	}
	if len(refl) > 0 && string(refl) != "null" {
		e.Reflection = &Reflection{}
		_ = json.Unmarshal(refl, e.Reflection)
	}
	_ = json.Unmarshal(usage, &e.Usage)
	if len(questions) > 0 && string(questions) != "null" {
		_ = json.Unmarshal(questions, &e.Questions)
	}
	_ = json.Unmarshal(clar, &e.Clarifications)
	if len(preview) > 0 && string(preview) != "null" {
		e.Preview = &Preview{}
		_ = json.Unmarshal(preview, e.Preview)
	}
	if len(e.Images) == 0 {
		e.Images = nil
	}
	return &e, nil
}

func (s *Store) CreateEvolution(ctx context.Context, e *Evolution) error {
	if e.ID == "" {
		e.ID = ids.New("evo")
	}
	if e.Status == "" {
		e.Status = Requested
	}
	if e.Kind == "" {
		e.Kind = "evolve"
	}
	if e.Checks == nil {
		e.Checks = []Check{}
	}
	images := e.Images
	if images == nil {
		images = []string{}
	}
	return s.Pool.QueryRow(ctx, `INSERT INTO evolutions (id, conversation_id, kind, intent, title, status, base_generation, target_generation, images)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8, $9) RETURNING created_at, updated_at`,
		e.ID, e.ConversationID, e.Kind, e.Intent, e.Title, e.Status, e.BaseGeneration, e.TargetGeneration, images).Scan(&e.CreatedAt, &e.UpdatedAt)
}

// SaveEvolution persists all mutable fields.
func (s *Store) SaveEvolution(ctx context.Context, e *Evolution) error {
	plan, _ := json.Marshal(e.Plan)
	checks, _ := json.Marshal(e.Checks)
	refl, _ := json.Marshal(e.Reflection)
	usage, _ := json.Marshal(e.Usage)
	var questions []byte
	if len(e.Questions) > 0 {
		questions, _ = json.Marshal(e.Questions)
	}
	if e.Clarifications == nil {
		e.Clarifications = []Clarification{}
	}
	clar, _ := json.Marshal(e.Clarifications)
	var preview []byte
	if e.Preview != nil {
		preview, _ = json.Marshal(e.Preview)
	}
	return s.Pool.QueryRow(ctx, `UPDATE evolutions SET title=$2, status=$3, plan=$4, base_generation=$5, new_generation=$6,
		base_commit=$7, branch=$8, worktree=$9, commit=$10, attempts=$11, checks=$12, reflection=$13, summary=$14,
		error=$15, usage=$16, completed_at=$17, questions=$18, clarifications=$19, preview=$20, updated_at=now() WHERE id=$1 RETURNING updated_at`,
		e.ID, e.Title, e.Status, plan, e.BaseGeneration, e.NewGeneration, e.BaseCommit, e.Branch, e.Worktree,
		e.Commit, e.Attempts, checks, refl, e.Summary, e.Error, usage, e.CompletedAt, questions, clar, preview).Scan(&e.UpdatedAt)
}

func (s *Store) Evolution(ctx context.Context, id string) (*Evolution, error) {
	return scanEvolution(s.Pool.QueryRow(ctx, `SELECT `+evoCols+` FROM evolutions WHERE id=$1`, id))
}

func (s *Store) Evolutions(ctx context.Context, limit int) ([]*Evolution, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+evoCols+` FROM evolutions ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Evolution
	for rows.Next() {
		e, err := scanEvolution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EvolutionsByStatus returns evolutions in any of the given states, oldest first.
func (s *Store) EvolutionsByStatus(ctx context.Context, states ...Status) ([]*Evolution, error) {
	strs := make([]string, len(states))
	for i, st := range states {
		strs[i] = string(st)
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+evoCols+` FROM evolutions WHERE status = ANY($1) ORDER BY created_at`, strs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Evolution
	for rows.Next() {
		e, err := scanEvolution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AddEvent(ctx context.Context, evoID, kind, summary string, data any) (*Event, error) {
	ev := &Event{EvolutionID: evoID, Kind: kind, Summary: summary, Data: data}
	raw, err := json.Marshal(data)
	if err != nil {
		raw = []byte(`null`)
	}
	err = s.Pool.QueryRow(ctx, `INSERT INTO evolution_events (evolution_id, kind, summary, data) VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`, evoID, kind, summary, raw).Scan(&ev.ID, &ev.CreatedAt)
	return ev, err
}

func (s *Store) Events(ctx context.Context, evoID string) ([]Event, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, evolution_id, kind, summary, data, created_at FROM evolution_events
		WHERE evolution_id=$1 ORDER BY id`, evoID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		var raw []byte
		err := r.Scan(&e.ID, &e.EvolutionID, &e.Kind, &e.Summary, &raw, &e.CreatedAt)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &e.Data)
		}
		return e, err
	})
}

// ---- generations

func (s *Store) AddGeneration(ctx context.Context, g *Generation) error {
	return s.Pool.QueryRow(ctx, `INSERT INTO generations (number, title, intent, evolution_id, commit, parent_commit, tests_passed, build_ok, health_ok)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9) ON CONFLICT (number) DO UPDATE SET commit = EXCLUDED.commit
		RETURNING created_at`,
		g.Number, g.Title, g.Intent, g.EvolutionID, g.Commit, g.ParentCommit, g.TestsPassed, g.BuildOK, g.HealthOK).Scan(&g.CreatedAt)
}

func (s *Store) Generations(ctx context.Context) ([]Generation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT number, title, intent, coalesce(evolution_id, ''), commit, parent_commit,
		tests_passed, build_ok, health_ok, created_at FROM generations ORDER BY number DESC`)
	if err != nil {
		return nil, err
	}
	gens, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Generation, error) {
		var g Generation
		err := r.Scan(&g.Number, &g.Title, &g.Intent, &g.EvolutionID, &g.Commit, &g.ParentCommit, &g.TestsPassed, &g.BuildOK, &g.HealthOK, &g.CreatedAt)
		return g, err
	})
	if len(gens) > 0 {
		gens[0].Current = true
	}
	return gens, err
}

// CurrentGeneration returns the highest generation, or ErrNotFound.
func (s *Store) CurrentGeneration(ctx context.Context) (*Generation, error) {
	gens, err := s.Generations(ctx)
	if err != nil {
		return nil, err
	}
	if len(gens) == 0 {
		return nil, ErrNotFound
	}
	return &gens[0], nil
}

func (s *Store) Generation(ctx context.Context, n int) (*Generation, error) {
	gens, err := s.Generations(ctx)
	if err != nil {
		return nil, err
	}
	for i := range gens {
		if gens[i].Number == n {
			return &gens[i], nil
		}
	}
	return nil, ErrNotFound
}

// ---- approvals

func (s *Store) CreateApproval(ctx context.Context, a *Approval) error {
	a.ID = ids.New("apr")
	a.Status = "pending"
	return s.Pool.QueryRow(ctx, `INSERT INTO approvals (id, evolution_id, action, level, detail) VALUES ($1, NULLIF($2, ''), $3, $4, $5)
		RETURNING created_at`, a.ID, a.EvolutionID, a.Action, a.Level, a.Detail).Scan(&a.CreatedAt)
}

func (s *Store) DecideApproval(ctx context.Context, id string, approved bool) (*Approval, error) {
	status := "denied"
	if approved {
		status = "approved"
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE approvals SET status=$2, decided_at=now() WHERE id=$1 AND status='pending'`, id, status)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.Approval(ctx, id)
}

func (s *Store) Approval(ctx context.Context, id string) (*Approval, error) {
	var a Approval
	err := s.Pool.QueryRow(ctx, `SELECT id, coalesce(evolution_id, ''), action, level, detail, status, created_at, decided_at
		FROM approvals WHERE id=$1`, id).Scan(&a.ID, &a.EvolutionID, &a.Action, &a.Level, &a.Detail, &a.Status, &a.CreatedAt, &a.DecidedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &a, err
}

func (s *Store) PendingApprovals(ctx context.Context) ([]Approval, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, coalesce(evolution_id, ''), action, level, detail, status, created_at, decided_at
		FROM approvals WHERE status='pending' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Approval, error) {
		var a Approval
		err := r.Scan(&a.ID, &a.EvolutionID, &a.Action, &a.Level, &a.Detail, &a.Status, &a.CreatedAt, &a.DecidedAt)
		return a, err
	})
}

// ExpireApprovals denies pending approvals (used at startup: nobody is waiting for them).
func (s *Store) ExpireApprovals(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `UPDATE approvals SET status='denied', decided_at=now() WHERE status='pending'`)
	return err
}

// ---- settings

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, key, value)
	return err
}

// ---- owner sessions

// OwnerSession is a browser my owner signed in with (hashes only).
type OwnerSession struct {
	ID         string    `json:"-"`
	APIHash    string    `json:"-"`
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (s *Store) CreateOwnerSession(ctx context.Context, o *OwnerSession) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO owner_sessions (id, api_hash, label, created_at, last_seen_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		o.ID, o.APIHash, o.Label, o.CreatedAt, o.LastSeenAt, o.ExpiresAt)
	return err
}

// OwnerSessions returns the sessions that have not expired, newest first.
func (s *Store) OwnerSessions(ctx context.Context) ([]*OwnerSession, error) {
	if _, err := s.Pool.Exec(ctx, `DELETE FROM owner_sessions WHERE expires_at < now()`); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, api_hash, label, created_at, last_seen_at, expires_at FROM owner_sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OwnerSession
	for rows.Next() {
		o := &OwnerSession{}
		if err := rows.Scan(&o.ID, &o.APIHash, &o.Label, &o.CreatedAt, &o.LastSeenAt, &o.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) TouchOwnerSession(ctx context.Context, id string, seen, expires time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE owner_sessions SET last_seen_at=$2, expires_at=$3 WHERE id=$1`, id, seen, expires)
	return err
}

func (s *Store) DeleteOwnerSession(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM owner_sessions WHERE id=$1`, id)
	return err
}

// ---- outbound access

// EgressGrant is one thing the owner allowed the live organism: a host it
// may reach, or a secret it may read.
type EgressGrant struct {
	Kind      string    `json:"kind"` // "host" or "secret"
	Value     string    `json:"value"`
	Reason    string    `json:"reason"`
	GrantedAt time.Time `json:"granted_at"`
}

func (s *Store) EgressGrants(ctx context.Context) ([]EgressGrant, error) {
	rows, err := s.Pool.Query(ctx, `SELECT kind, value, reason, granted_at FROM egress_grants ORDER BY kind, value`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EgressGrant
	for rows.Next() {
		var g EgressGrant
		if err := rows.Scan(&g.Kind, &g.Value, &g.Reason, &g.GrantedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) AddEgressGrant(ctx context.Context, kind, value, reason string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO egress_grants (kind, value, reason) VALUES ($1,$2,$3) ON CONFLICT (kind, value) DO UPDATE SET reason=EXCLUDED.reason`, kind, value, reason)
	return err
}

func (s *Store) DeleteEgressGrant(ctx context.Context, kind, value string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM egress_grants WHERE kind=$1 AND value=$2`, kind, value)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ---- routines

// Routine is something I do on a schedule.
type Routine struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`   // "agent" or "job"
	Source      string     `json:"source"` // "owner" or "organism"
	Schedule    string     `json:"schedule"`
	Timezone    string     `json:"timezone"`
	Prompt      string     `json:"prompt,omitempty"`
	Method      string     `json:"method,omitempty"`
	Path        string     `json:"path,omitempty"`
	Description string     `json:"description,omitempty"`
	Enabled     bool       `json:"enabled"`
	NextRunAt   *time.Time `json:"next_run_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// RoutineRun is one run of a routine.
type RoutineRun struct {
	ID         string     `json:"id"`
	RoutineID  string     `json:"routine_id"`
	Trigger    string     `json:"trigger"` // "schedule" or "manual"
	Status     string     `json:"status"`  // running, ok, quiet, failed
	Output     string     `json:"output"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

const routineCols = `id, name, kind, source, schedule, timezone, prompt, method, path, description, enabled, next_run_at, created_at`

func scanRoutine(row pgx.Row) (*Routine, error) {
	r := &Routine{}
	err := row.Scan(&r.ID, &r.Name, &r.Kind, &r.Source, &r.Schedule, &r.Timezone, &r.Prompt, &r.Method, &r.Path, &r.Description, &r.Enabled, &r.NextRunAt, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) SaveRoutine(ctx context.Context, r *Routine) error {
	if r.ID == "" {
		r.ID = ids.New("rtn")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO routines (`+routineCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, schedule=EXCLUDED.schedule, timezone=EXCLUDED.timezone, prompt=EXCLUDED.prompt,
		method=EXCLUDED.method, path=EXCLUDED.path, description=EXCLUDED.description, enabled=EXCLUDED.enabled, next_run_at=EXCLUDED.next_run_at, updated_at=now()`,
		r.ID, r.Name, r.Kind, r.Source, r.Schedule, r.Timezone, r.Prompt, r.Method, r.Path, r.Description, r.Enabled, r.NextRunAt, r.CreatedAt)
	return err
}

func (s *Store) Routine(ctx context.Context, id string) (*Routine, error) {
	return scanRoutine(s.Pool.QueryRow(ctx, `SELECT `+routineCols+` FROM routines WHERE id=$1`, id))
}

func (s *Store) Routines(ctx context.Context) ([]*Routine, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+routineCols+` FROM routines ORDER BY source, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Routine
	for rows.Next() {
		r, err := scanRoutine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DueRoutines are enabled routines whose next run has come.
func (s *Store) DueRoutines(ctx context.Context, now time.Time) ([]*Routine, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+routineCols+` FROM routines WHERE enabled AND next_run_at <= $1 ORDER BY next_run_at`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Routine
	for rows.Next() {
		r, err := scanRoutine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SetRoutineNextRun(ctx context.Context, id string, next *time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE routines SET next_run_at=$2 WHERE id=$1`, id, next)
	return err
}

func (s *Store) DeleteRoutine(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM routines WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) StartRoutineRun(ctx context.Context, routineID, trigger string) (*RoutineRun, error) {
	r := &RoutineRun{ID: ids.New("run"), RoutineID: routineID, Trigger: trigger, Status: "running"}
	err := s.Pool.QueryRow(ctx, `INSERT INTO routine_runs (id, routine_id, trigger, status) VALUES ($1,$2,$3,'running') RETURNING started_at`,
		r.ID, routineID, trigger).Scan(&r.StartedAt)
	return r, err
}

func (s *Store) FinishRoutineRun(ctx context.Context, r *RoutineRun) error {
	now := time.Now()
	r.FinishedAt = &now
	_, err := s.Pool.Exec(ctx, `UPDATE routine_runs SET status=$2, output=$3, finished_at=$4 WHERE id=$1`, r.ID, r.Status, r.Output, now)
	if err == nil {
		// Keep the last 100 runs of each routine.
		_, err = s.Pool.Exec(ctx, `DELETE FROM routine_runs WHERE routine_id=$1 AND id NOT IN
			(SELECT id FROM routine_runs WHERE routine_id=$1 ORDER BY started_at DESC LIMIT 100)`, r.RoutineID)
	}
	return err
}

// FailInterruptedRuns marks runs left "running" by a previous kernel as failed.
func (s *Store) FailInterruptedRuns(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `UPDATE routine_runs SET status='failed', output='interrupted: I restarted while this ran', finished_at=now() WHERE status='running'`)
	return err
}

func (s *Store) RoutineRuns(ctx context.Context, routineID string, limit int) ([]RoutineRun, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, routine_id, trigger, status, output, started_at, finished_at FROM routine_runs WHERE routine_id=$1 ORDER BY started_at DESC LIMIT $2`, routineID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoutineRun
	for rows.Next() {
		var r RoutineRun
		if err := rows.Scan(&r.ID, &r.RoutineID, &r.Trigger, &r.Status, &r.Output, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddRoutineReport records what a routine has to tell my owner.
func (s *Store) AddRoutineReport(ctx context.Context, conv, content string) (*Message, error) {
	return s.addMessage(ctx, conv, "seed", "routine", content, "")
}

// ---- incidents

// Incident is something going wrong in the live organism.
type Incident struct {
	ID          string     `json:"id"`
	Signature   string     `json:"signature"`
	Kind        string     `json:"kind"` // http, crash, job
	Title       string     `json:"title"`
	Count       int        `json:"count"`
	FirstSeen   time.Time  `json:"first_seen"`
	LastSeen    time.Time  `json:"last_seen"`
	Evidence    string     `json:"evidence"`
	Status      string     `json:"status"` // open, diagnosing, diagnosed, fixing, watching, resolved, ignored
	Diagnosis   string     `json:"diagnosis"`
	Fix         string     `json:"fix"`
	Note        string     `json:"note"`
	EvolutionID string     `json:"evolution_id"`
	FixedAt     *time.Time `json:"fixed_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
}

const incidentCols = `id, signature, kind, title, count, first_seen, last_seen, evidence, status, diagnosis, fix, note, evolution_id, fixed_at, resolved_at`

func scanIncident(row pgx.Row) (*Incident, error) {
	i := &Incident{}
	err := row.Scan(&i.ID, &i.Signature, &i.Kind, &i.Title, &i.Count, &i.FirstSeen, &i.LastSeen, &i.Evidence, &i.Status,
		&i.Diagnosis, &i.Fix, &i.Note, &i.EvolutionID, &i.FixedAt, &i.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return i, err
}

// LiveIncident is the unresolved, not-ignored incident with a signature.
func (s *Store) LiveIncident(ctx context.Context, signature string) (*Incident, error) {
	return scanIncident(s.Pool.QueryRow(ctx, `SELECT `+incidentCols+` FROM incidents WHERE signature=$1 AND status NOT IN ('resolved','ignored')`, signature))
}

// IgnoredIncident reports whether my owner muted a signature.
func (s *Store) IgnoredIncident(ctx context.Context, signature string) bool {
	var n int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM incidents WHERE signature=$1 AND status='ignored'`, signature).Scan(&n)
	return n > 0
}

func (s *Store) Incident(ctx context.Context, id string) (*Incident, error) {
	return scanIncident(s.Pool.QueryRow(ctx, `SELECT `+incidentCols+` FROM incidents WHERE id=$1`, id))
}

func (s *Store) Incidents(ctx context.Context, limit int) ([]*Incident, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+incidentCols+` FROM incidents ORDER BY (status IN ('resolved','ignored')), last_seen DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Incident
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// SaveIncident inserts or updates an incident.
func (s *Store) SaveIncident(ctx context.Context, i *Incident) error {
	if i.ID == "" {
		i.ID = ids.New("inc")
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO incidents (`+incidentCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, count=EXCLUDED.count, last_seen=EXCLUDED.last_seen, evidence=EXCLUDED.evidence,
		status=EXCLUDED.status, diagnosis=EXCLUDED.diagnosis, fix=EXCLUDED.fix, note=EXCLUDED.note, evolution_id=EXCLUDED.evolution_id,
		fixed_at=EXCLUDED.fixed_at, resolved_at=EXCLUDED.resolved_at`,
		i.ID, i.Signature, i.Kind, i.Title, i.Count, i.FirstSeen, i.LastSeen, i.Evidence, i.Status, i.Diagnosis, i.Fix, i.Note, i.EvolutionID, i.FixedAt, i.ResolvedAt)
	return err
}

// IncidentsWithStatus returns incidents in any of the statuses.
func (s *Store) IncidentsWithStatus(ctx context.Context, statuses ...string) ([]*Incident, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+incidentCols+` FROM incidents WHERE status = ANY($1) ORDER BY last_seen`, statuses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Incident
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// AddHealthReport records what my doctor found (written from my app's logs).
func (s *Store) AddHealthReport(ctx context.Context, conv, content string) (*Message, error) {
	return s.addMessage(ctx, conv, "seed", "health", content, "")
}
