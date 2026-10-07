# Control Plane API

The kernel serves the control plane at `/_seed` and its JSON API at `/_seed/api`.
Everything outside `/_seed` is reverse-proxied to the running organism.

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
};

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
  created_at: string;
  updated_at: string;
  completed_at?: string;
};

type EvolutionEvent = {
  id: number;
  evolution_id: string;
  kind: "phase" | "plan" | "tool_call" | "tool_result" | "check" | "note" | "error" | "approval" | "agent_text";
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

The owner picks the model that drives the Seed. A fresh Seed **always asks**: it has no mind
until the owner chooses a provider and gives a key. Environment variables are never picked up
implicitly. API keys are stored per provider in the owner's user config
(`~/.config/seed/credentials.json`, mode 0600), so they can be reused across Seeds and more
providers and keys can be added over time. Keys are **never** returned by the API. The per-Seed
choice (provider and model) is kept in the Seed's operational memory. Changes take effect
immediately, with no restart.

```ts
type ProviderInfo = {
  id: string;                      // v0.1 offers "openrouter" only (one key, any model); more providers later
  label: string;                   // "Anthropic", "OpenRouter", "OpenAI", "Local / OpenAI-compatible"
  has_key: boolean;                // a key for this provider is stored in the owner's credentials
  needs_key: boolean;              // false for openai-compatible
  needs_base_url: boolean;         // true for openai-compatible
  base_url: string;                // stored base URL (openai-compatible), "" otherwise
  default_model: string;
};

type ModelConfig = {
  provider: string;                // currently selected provider id
  name: string;                    // currently selected model id
  configured: boolean;             // the owner chose a provider/model for this Seed and a key exists
  providers: ProviderInfo[];
};

type ModelOption = { id: string; name: string; context_length?: number; description?: string };
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/model` | | `ModelConfig` |
| GET | `/model/options?provider=<id>` | | `{models: ModelOption[], error?: string}` — models the provider offers (fetched live when possible, curated fallback otherwise) |
| POST | `/model` | `{provider, name, api_key?, base_url?}` | `ModelConfig` — validates by making a tiny test call; on failure returns 400 `{error}` and changes nothing. `api_key` (if given) is stored for that provider. |
| POST | `/model/forget-key` | `{provider}` | `ModelConfig` — deletes the stored key for that provider |

`Status.model` (`{provider, name, configured}`) reflects changes; a `status` SSE event is sent after a change.
