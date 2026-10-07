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
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	EvolutionID    string    `json:"evolution_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type Plan struct {
	Title        string     `json:"title"`
	Scope        string     `json:"scope"`
	Summary      string     `json:"summary"`
	Steps        []PlanStep `json:"steps"`
	Capabilities []string   `json:"capabilities"`
	Risks        []string   `json:"risks"`
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
	Summary       string   `json:"summary"`
	GoalSatisfied bool     `json:"goal_satisfied"`
	Decisions     []string `json:"decisions"`
	Learnings     []string `json:"learnings"`
	Skills        []string `json:"skills"`
	Debt          []string `json:"debt"`
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
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
	CompletedAt      *time.Time  `json:"completed_at,omitempty"`
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
	m := &Message{ID: ids.New("msg"), ConversationID: conv, Role: role, Content: content, EvolutionID: evolutionID}
	err := s.Pool.QueryRow(ctx, `INSERT INTO messages (id, conversation_id, role, content, evolution_id)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')) RETURNING created_at`, m.ID, conv, role, content, evolutionID).Scan(&m.CreatedAt)
	return m, err
}

// Messages returns the last limit messages, oldest first.
func (s *Store) Messages(ctx context.Context, conv string, limit int) ([]Message, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, conversation_id, role, content, coalesce(evolution_id, ''), created_at FROM (
		SELECT * FROM messages WHERE conversation_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2) m
		ORDER BY created_at, id`, conv, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) {
		var m Message
		err := r.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.EvolutionID, &m.CreatedAt)
		return m, err
	})
}

// ---- evolutions

const evoCols = `id, coalesce(conversation_id, ''), kind, intent, title, status, plan, base_generation, new_generation,
	target_generation, base_commit, branch, worktree, commit, attempts, checks, reflection, summary, error, usage,
	created_at, updated_at, completed_at`

func scanEvolution(r pgx.Row) (*Evolution, error) {
	var e Evolution
	var plan, checks, refl, usage []byte
	err := r.Scan(&e.ID, &e.ConversationID, &e.Kind, &e.Intent, &e.Title, &e.Status, &plan, &e.BaseGeneration,
		&e.NewGeneration, &e.TargetGeneration, &e.BaseCommit, &e.Branch, &e.Worktree, &e.Commit, &e.Attempts,
		&checks, &refl, &e.Summary, &e.Error, &usage, &e.CreatedAt, &e.UpdatedAt, &e.CompletedAt)
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
	return s.Pool.QueryRow(ctx, `INSERT INTO evolutions (id, conversation_id, kind, intent, title, status, base_generation, target_generation)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8) RETURNING created_at, updated_at`,
		e.ID, e.ConversationID, e.Kind, e.Intent, e.Title, e.Status, e.BaseGeneration, e.TargetGeneration).Scan(&e.CreatedAt, &e.UpdatedAt)
}

// SaveEvolution persists all mutable fields.
func (s *Store) SaveEvolution(ctx context.Context, e *Evolution) error {
	plan, _ := json.Marshal(e.Plan)
	checks, _ := json.Marshal(e.Checks)
	refl, _ := json.Marshal(e.Reflection)
	usage, _ := json.Marshal(e.Usage)
	return s.Pool.QueryRow(ctx, `UPDATE evolutions SET title=$2, status=$3, plan=$4, base_generation=$5, new_generation=$6,
		base_commit=$7, branch=$8, worktree=$9, commit=$10, attempts=$11, checks=$12, reflection=$13, summary=$14,
		error=$15, usage=$16, completed_at=$17, updated_at=now() WHERE id=$1 RETURNING updated_at`,
		e.ID, e.Title, e.Status, plan, e.BaseGeneration, e.NewGeneration, e.BaseCommit, e.Branch, e.Worktree,
		e.Commit, e.Attempts, checks, refl, e.Summary, e.Error, usage, e.CompletedAt).Scan(&e.UpdatedAt)
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
