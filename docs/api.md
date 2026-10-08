# Control Plane API

The kernel serves the control plane at `/_seed` and its JSON API at `/_seed/api`.
Everything outside `/_seed` is reverse-proxied to the running organism.

Every endpoint except the logo needs `X-Seed-Token` (or `?token=` for the event stream): the
API token embedded in the control-plane page of a signed-in browser, or the CLI's token from
`.seed/control-token`. Sign-in itself happens at `GET /_seed/login?code=` (a one-time link; it
sets the session cookie and redirects to `/_seed/`).

All responses are JSON. Errors are `{"error": "message"}` with a non-2xx status.
Timestamps are RFC 3339 strings.

## Types

```ts
type Identity = {
  name: string;                    // display name the Seed currently goes by ("Seed" until it becomes something)
  tagline: string;
  accent: string;                  // CSS color, e.g. "#3b82f6" ("" = default)
  logo_url: string;                // "/_seed/api/identity/logo?v=<hash>" (SVG; changes when the logo changes)
};

type Status = {
  name: string;                    // technical slug from seed.yaml (stable)
  identity: Identity;              // who the Seed currently is — evolves with generations
  purpose: string;                 // "" while the Seed has no purpose yet
  generation: Generation | null;   // current generation
  organism: { state: "stopped" | "building" | "starting" | "running" | "failed"; error?: string };
  model: { provider: string; name: string; configured: boolean };
  active_evolution: Evolution | null;
  pending_approvals: number;
};

type Message = {
  id: string;
  conversation_id: string;
  role: "user" | "seed" | "system";
  content: string;                  // markdown
  evolution_id?: string;            // set when the message announces/tracks an evolution
  kind: "chat" | "report";          // report = written by the kernel about an evolution
  created_at: string;
};

type EvolutionStatus =
  | "requested" | "planning" | "planned" | "mutating" | "building" | "testing"
  | "running" | "observing" | "reflecting" | "ready" | "applying" | "complete"
  | "failed" | "needs_input" | "cancelled" | "rolled_back";

type Plan = {
  title: string;
  scope: string;                    // short slug, e.g. "tasks"
  summary: string;
  steps: { title: string; detail?: string }[];
  capabilities: string[];           // capabilities the Seed will have afterwards
  risks: string[];
  // Staging (absent for single-step evolutions): the owner's whole goal, the
  // full ordered roadmap, and the 1-based stage this evolution builds.
  goal?: string;
  stages?: { title: string; summary?: string }[];
  stage?: number;
};

type Question = { question: string; why?: string; options?: string[] };  // options: suggested answers, recommended first
type Clarification = { questions: Question[]; answer: string };

type Check = {
  name: string;                     // build | migrate | test | launch | health | http:<path>
  ok: boolean;
  detail: string;                   // short human-readable result
  attempt: number;
  tests_passed?: number;
  tests_failed?: number;
};

type Reflection = {
  summary: string;
  goal_satisfied: boolean;
  gaps?: string[];                  // when not fully satisfied: what falls short, and why
  decisions: string[];
  learnings: string[];
  skills: string[];                 // skills created or improved
  debt: string[];
};

type Evolution = {
  id: string;                       // evo_...
  intent: string;
  title: string;
  status: EvolutionStatus;
  plan: Plan | null;
  base_generation: number;
  new_generation?: number;
  branch: string;
  commit?: string;
  attempts: number;
  checks: Check[];
  reflection: Reflection | null;
  summary?: string;                 // what the builder says it did
  error?: string;
  kind: "evolve" | "rollback";
  questions?: Question[];           // set while status is "needs_input" because the Seed asked its owner
  clarifications?: Clarification[]; // earlier question/answer rounds
  created_at: string;
  updated_at: string;
  completed_at?: string;
};

type EvolutionEvent = {
  id: number;
  evolution_id: string;
  kind: "phase" | "plan" | "tool_call" | "tool_result" | "check" | "note" | "error" | "approval" | "agent_text" | "question";
  summary: string;                  // one line, suitable for a progress list
  data: any;                        // details (tool input/output, etc.)
  created_at: string;
};

type Generation = {
  number: number;
  title: string;
  intent: string;
  evolution_id?: string;
  commit: string;
  parent_commit?: string;
  current: boolean;
  tests_passed?: number;
  build_ok?: boolean;
  health_ok?: boolean;
  created_at: string;
};

type Approval = {
  id: string;
  evolution_id?: string;
  action: string;                   // e.g. "write_file kernel/agent/loop.go"
  level: "safe" | "review" | "dangerous";
  detail: string;
  status: "pending" | "approved" | "denied";
  created_at: string;
};

type Skill = { name: string; description: string; path: string; files: string[]; content?: string };
```

## Endpoints

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/status` | | `Status` |
| GET | `/identity/logo` | | the current logo (`image/svg+xml`) |
| GET | `/messages?limit=200` | | `Message[]` (oldest first) |
| POST | `/messages` | `{content}` | `Message` (the stored user message; replies arrive via SSE) |
| GET | `/evolutions` | | `Evolution[]` (newest first) |
| POST | `/evolutions` | `{intent}` | `Evolution` |
| GET | `/evolutions/:id` | | `{evolution: Evolution, events: EvolutionEvent[]}` |
| GET | `/evolutions/:id/diff` | | `{stat: string, diff: string}` |
| POST | `/evolutions/:id/cancel` | | `Evolution` |
| POST | `/evolutions/:id/answer` | `{answer}` | `Message` (stored as the owner's message). 409 if the evolution is not waiting. While an evolution waits on questions, a normal `POST /messages` is also routed to it as the answer. |
| GET | `/approvals` | | `Approval[]` (pending) |
| POST | `/approvals/:id` | `{approved: boolean}` | `Approval` |
| GET | `/generations` | | `Generation[]` (newest first) |
| POST | `/generations/:n/rollback` | | `Evolution` (a `kind: "rollback"` evolution) |
| GET | `/skills` | | `Skill[]` |
| GET | `/skills/:name` | | `Skill` with `content` |
| GET | `/knowledge` | | `{self: string, docs: {path,title}[], decisions: {path,title}[]}` |
| GET | `/knowledge/file?path=` | | `{path, content}` |
| GET | `/logs?source=organism\|kernel&tail=500` | | `{lines: string[]}` |
| GET | `/settings` | | redacted configuration object |
| GET | `/extensions` | | `{id, title, path}[]` — control-plane pages contributed by the organism |
| GET | `/routines` | | `Routine[]` with `schedule_text` and `last_run` (see [routines](routines.md)) |
| POST | `/routines` | `{name, kind: "agent"\|"job", schedule, timezone, prompt?, method?, path?}` | `Routine` |
| POST | `/routines/:id/update` | `{enabled?, name?, schedule?, timezone?, prompt?, method?, path?}` | `Routine`. Only the owner's own routines can be edited; the organism's jobs can be paused. |
| POST | `/routines/:id/run` | | `RoutineRun` (started now; 409 if already running) |
| POST | `/routines/:id/delete` | | `{ok}` (owner's routines only) |
| GET | `/routines/:id/runs` | | `RoutineRun[]` (newest first, up to 30) |
| GET | `/outbound` | | `{hosts, secrets, denied, available_secrets}`: what the live organism may reach and read, and recently refused connections |
| POST | `/outbound/grant` | `{hosts?, secrets?, reason?}` | same as GET, after granting |
| POST | `/outbound/revoke` | `{kind: "host"\|"secret", value}` | same as GET, after revoking |
| POST | `/login-links` | | `{path, expires_at}`: a one-time sign-in link (one use, 15 minutes) |
| GET | `/owner/sessions` | | `{id, label, created_at, last_seen_at, current}[]`: signed-in browsers |
| POST | `/owner/sessions/:id/revoke` | | `{ok}`: signs that browser out |
| POST | `/owner/logout` | | `{ok}`: signs out the calling browser and clears its cookie |

## Live events

`GET /_seed/api/events` is a Server-Sent Events stream. Each event has an `event:` name and JSON `data:`.

| event | data |
|---|---|
| `message` | `Message` |
| `evolution` | `Evolution` (full object, sent on every change) |
| `evolution_event` | `EvolutionEvent` |
| `approval` | `Approval` |
| `status` | `Status` |
| `chat` | `{thinking: boolean}` |

## Model ("mind") configuration

A Seed stores **no credentials**. The owner passes secrets as key/value pairs when starting it
(`seed run -e OPENROUTER_API_KEY`, `--env-file`); a deployed Seed gets the same variables from its
platform. The kernel holds them in memory only. If no key was passed, the owner may paste one in the
control plane **for this session only** (memory; gone on restart). Keys are never returned by the API.
The model choice (not a secret) is kept in the Seed's memory, and `SEED_MODEL` can also be passed at start.
When a key is present and no model was chosen, the provider's default model is used.

```ts
type ProviderInfo = {
  id: string;                      // v0.1: "openrouter" only
  label: string;
  has_key: boolean;                // a key is available right now
  key_source: "env" | "session" | "";   // passed at start, pasted this session, or none
  key_env: string;                 // the variable to pass, e.g. "OPENROUTER_API_KEY"
  needs_key: boolean;
  needs_base_url: boolean;
  base_url: string;
  default_model: string;
  keys_url?: string;               // where to get a key
};

type ModelConfig = {
  provider: string;                // selected provider id
  name: string;                    // selected model id (the default when none was chosen)
  configured: boolean;             // the Seed can think (a key is present)
  providers: ProviderInfo[];
};

type ModelOption = { id: string; name: string; context_length?: number; description?: string };
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/model` | | `ModelConfig` |
| GET | `/model/options?provider=<id>` | | `{models: ModelOption[], error?: string}` |
| POST | `/model` | `{provider, name, api_key?}` | `ModelConfig`. Validates with a tiny test call; 400 `{error}` changes nothing. `api_key` is held **for this session only**. |
| POST | `/model/forget-key` | `{provider}` | `ModelConfig`. Forgets a session key (keys passed at start cannot be forgotten). |

`Status.model` (`{provider, name, configured}`) reflects changes; a `status` SSE event is sent after a change.
