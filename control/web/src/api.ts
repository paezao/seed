// Typed client for the Seed kernel control-plane API (docs/api.md).

export const API_BASE = '/_seed/api';

/** The kernel embeds this signed-in browser's API token in the page; every API call carries it. */
export const CONTROL_TOKEN =
  document.querySelector<HTMLMetaElement>('meta[name="seed-token"]')?.content ?? '';

export type OwnerSession = {
  id: string;
  label: string;
  created_at: string;
  last_seen_at: string;
  current: boolean;
};

export type OutboundGrant = { kind: 'host' | 'secret'; value: string; reason: string; granted_at: string };
export type Outbound = {
  hosts: OutboundGrant[];
  secrets: (OutboundGrant & { passed: boolean })[];
  denied: { host: string; reason: string; at: string }[];
  available_secrets: string[];
};

export type RoutineRun = {
  id: string;
  routine_id: string;
  trigger: 'schedule' | 'manual';
  status: 'running' | 'ok' | 'quiet' | 'failed' | 'skipped';
  output: string;
  started_at: string;
  finished_at: string | null;
};
export type Routine = {
  id: string;
  name: string;
  kind: 'agent' | 'job';
  source: 'owner' | 'organism';
  schedule: string;
  schedule_text: string;
  timezone: string;
  prompt?: string;
  method?: string;
  path?: string;
  description?: string;
  enabled: boolean;
  next_run_at: string | null;
  last_run: RoutineRun | null;
};
export type NewRoutine = { name: string; kind: 'agent' | 'job'; schedule: string; timezone: string; prompt?: string; method?: string; path?: string };

export type KernelRelease = { version: string; published_at: string; notes: string };
export type KernelStatus = {
  version: string;
  latest: KernelRelease | null;
  available: boolean;
  needs_runtime: boolean;
  checked_at: string | null;
  check_error?: string;
  applying: boolean;
  last_event: { ok: boolean; message: string; log?: string; at: string } | null;
};

export type IncidentStatus = 'open' | 'diagnosing' | 'diagnosed' | 'fixing' | 'watching' | 'resolved' | 'ignored';
export type Incident = {
  id: string;
  kind: 'http' | 'crash' | 'job';
  title: string;
  count: number;
  first_seen: string;
  last_seen: string;
  evidence: string;
  status: IncidentStatus;
  diagnosis: string;
  fix: string;
  note: string;
  evolution_id: string;
  fixed_at: string | null;
  resolved_at: string | null;
};

/** The control plane version this page was loaded with (stamped by the kernel). */
export const UI_VERSION = document.querySelector<HTMLMetaElement>('meta[name="seed-ui"]')?.content ?? '';

export type OrganismState = 'stopped' | 'building' | 'starting' | 'running' | 'failed';

export type Identity = {
  name: string;
  tagline: string;
  accent: string;
  logo_url: string;
};

export type Status = {
  name: string;
  identity: Identity;
  purpose: string;
  generation: Generation | null;
  organism: { state: OrganismState; error?: string };
  model: { provider: string; name: string; configured: boolean };
  active_evolution: Evolution | null;
  pending_approvals: number;
  open_incidents?: number;
  /** This month's spending on models, and the budget (if any). */
  spend?: { month_usd: number; budget_usd?: number; paused?: boolean };
  ui_version?: string;
};

export type PushDevice = { id: string; user_agent: string; created_at: string; last_sent_at?: string; last_error?: string };
export type NotificationsInfo = {
  public_key: string;
  subscriptions: PushDevice[];
  kinds: Record<string, boolean>;
  about: { key: string; label: string; about: string; default: boolean }[];
};

export type Backup = {
  id: string;
  kind: 'before_generation' | 'daily' | 'manual' | 'before_restore';
  generation: number;
  label?: string;
  size: number;
  created_at: string;
};
export type BackupsInfo = { backups: Backup[]; daily: boolean; unavailable: string; keep: Record<string, number> };

/** Saves a backup's file (the kernel needs the token header, so it's fetched). */
export async function downloadBackup(id: string): Promise<void> {
  const res = await fetch(`${API_BASE}/backups/${encodeURIComponent(id)}/download`, { headers: { 'X-Seed-Token': CONTROL_TOKEN } });
  if (!res.ok) throw new Error(res.status === 404 ? 'That backup is gone.' : `HTTP ${res.status}`);
  const name = /filename="([^"]+)"/.exec(res.headers.get('content-disposition') || '')?.[1] || `${id}.dump`;
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

/** The copy that holds the data of generation n (the last one taken while it was live). */
export function backupOf(backups: Backup[], n: number): Backup | undefined {
  return backups.find((b) => b.generation === n && b.kind !== 'before_restore');
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

export type SpendTotal = { key: string; ref?: string; label?: string; cost_usd: number; calls: number };
export type Spending = {
  month: {
    from: string;
    total_usd: number;
    calls: number;
    unpriced_calls: number;
    by_kind: SpendTotal[];
    by_day: SpendTotal[];
    top: SpendTotal[];
  };
  budget_usd: number;
  paused: boolean;
};

/** Dollars, with cents (or more precision for tiny amounts). */
export function usd(v: number): string {
  if (v > 0 && v < 0.01) return '<$0.01';
  return '$' + v.toFixed(2);
}

export type Message = {
  id: string;
  conversation_id: string;
  role: 'user' | 'seed' | 'system';
  content: string;
  evolution_id?: string;
  /** Images my owner attached (ids; see imageBlobURL). */
  images?: string[];
  kind?: 'chat' | 'report' | 'routine';
  created_at: string;
};

export type EvolutionStatus =
  | 'requested' | 'planning' | 'planned' | 'mutating' | 'building' | 'testing'
  | 'running' | 'observing' | 'reflecting' | 'ready' | 'applying' | 'complete'
  | 'failed' | 'needs_input' | 'cancelled' | 'rolled_back';

export type PlanStep = { title: string; detail?: string };

export type Plan = {
  title: string;
  scope: string;
  summary: string;
  steps: PlanStep[];
  capabilities: string[];
  risks: string[];
  /** Staging (absent for single-step evolutions): the owner's whole goal, the full ordered roadmap, and the 1-based stage this evolution builds. */
  goal?: string;
  stages?: PlanStage[];
  stage?: number;
};

export type PlanStage = { title: string; summary?: string };

/** Something the Seed asks its owner. `options` are suggested answers, recommended first. */
export type Question = { question: string; why?: string; options?: string[] };
/** An earlier question/answer round. */
export type Clarification = { questions: Question[]; answer: string };

export type Check = {
  name: string;
  ok: boolean;
  detail: string;
  attempt: number;
  tests_passed?: number;
  tests_failed?: number;
};

export type Reflection = {
  summary: string;
  goal_satisfied: boolean;
  gaps?: string[];
  decisions: string[];
  learnings: string[];
  skills: string[];
  debt: string[];
};

export type Evolution = {
  id: string;
  intent: string;
  title: string;
  status: EvolutionStatus;
  plan: Plan | null;
  base_generation: number;
  new_generation?: number;
  branch: string;
  commit?: string;
  attempts: number;
  usage?: { input_tokens: number; output_tokens: number; model_calls: number; cost_usd?: number };
  checks: Check[];
  reflection: Reflection | null;
  summary?: string;
  error?: string;
  kind: 'evolve' | 'rollback';
  /** Set while status is "needs_input" because the Seed asked its owner. */
  questions?: Question[];
  clarifications?: Clarification[];
  preview?: { skip?: boolean; state?: 'starting' | 'ready' | 'failed' | 'done'; data?: 'copy' | 'fresh'; note?: string; round?: number };
  created_at: string;
  updated_at: string;
  completed_at?: string;
};

export type EvolutionEventKind =
  | 'phase' | 'plan' | 'tool_call' | 'tool_result' | 'check' | 'note' | 'error' | 'approval' | 'agent_text' | 'question';

export type EvolutionEvent = {
  id: number;
  evolution_id: string;
  kind: EvolutionEventKind;
  summary: string;
  data: unknown;
  created_at: string;
};

export type Generation = {
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

export type Approval = {
  id: string;
  evolution_id?: string;
  action: string;
  level: 'safe' | 'review' | 'dangerous';
  detail: string;
  status: 'pending' | 'approved' | 'denied';
  created_at: string;
};

export type Skill = { name: string; description: string; path: string; files: string[]; content?: string };

export type DocRef = { path: string; title: string };
export type Knowledge = { self: string; self_model: Record<string, unknown> | null; docs: DocRef[]; decisions: DocRef[] };
export type KnowledgeFile = { path: string; content: string };
export type Diff = { stat: string; diff: string };
export type LogSource = 'organism' | 'kernel';
export type Logs = { lines: string[] };
export type Extension = { id: string; title: string; path: string };
export type Settings = Record<string, unknown>;

// ---- model ("mind") configuration ----
export type ProviderId = 'anthropic' | 'openrouter' | 'openai' | 'openai-compatible';
export type KeySource = 'env' | 'session' | '';
export type ProviderInfo = {
  id: ProviderId;
  label: string;
  /** A key is available right now (never returned). */
  has_key: boolean;
  /** Where the key came from: passed at start ("env"), lent this session ("session"), or none. */
  key_source?: KeySource;
  /** The variable to pass at start, e.g. "OPENROUTER_API_KEY". */
  key_env?: string;
  needs_key: boolean;
  needs_base_url: boolean;
  base_url: string;
  default_model: string;
  /** Where to get a key. */
  keys_url?: string;
};
export type ModelConfig = {
  provider: string;
  name: string;
  configured: boolean;
  providers: ProviderInfo[];
};
export type ModelOption = { id: string; name: string; context_length?: number; description?: string };
export type ModelOptions = { models: ModelOption[]; error?: string };
export type SetModelBody = { provider: string; name: string; api_key?: string; base_url?: string };

// ---- SSE event payloads ----
export type LiveEventMap = {
  message: Message;
  evolution: Evolution;
  evolution_event: EvolutionEvent;
  approval: Approval;
  status: Status;
  chat: { thinking: boolean };
  routine: { id?: string; run?: RoutineRun; deleted?: boolean; synced?: boolean };
  kernel: KernelStatus;
  incident: Incident;
  backup: Backup;
  restore: { id: string; state: 'restoring' | 'done' | 'failed' };
};
export type LiveEventName = keyof LiveEventMap;
export type LiveEvent = { [K in LiveEventName]: { type: K; data: LiveEventMap[K] } }[LiveEventName];
export const LIVE_EVENT_NAMES: LiveEventName[] = ['message', 'evolution', 'evolution_event', 'approval', 'status', 'chat', 'routine', 'kernel', 'incident', 'backup', 'restore'];

// ---- errors ----

/** Thrown for non-2xx responses from a reachable kernel. */
export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = 'ApiError';
  }
}

/** Thrown when the kernel cannot be reached at all (network failure, proxy 502/503/504). */
export class NetworkError extends Error {
  constructor(message = 'Kernel unreachable') {
    super(message);
    this.name = 'NetworkError';
  }
}

type Listener = (reachable: boolean) => void;
const reachabilityListeners = new Set<Listener>();
/** Subscribe to reachability changes observed by any API call. */
export function onReachability(fn: Listener): () => void {
  reachabilityListeners.add(fn);
  return () => reachabilityListeners.delete(fn);
}
function reportReachable(ok: boolean) {
  reachabilityListeners.forEach((fn) => fn(ok));
}

/**
 * Loads an image from the kernel (which needs the token header, so no plain
 * <img src>) as an object URL. Revoke it when done.
 */
export async function imageBlobURL(path: string): Promise<string> {
  const res = await fetch(API_BASE + path, { headers: { 'X-Seed-Token': CONTROL_TOKEN } });
  if (!res.ok) throw new Error(res.status === 404 ? 'gone' : `HTTP ${res.status}`);
  const type = res.headers.get('content-type') || '';
  if (type !== 'image/png' && type !== 'image/jpeg') throw new Error('not an image');
  return URL.createObjectURL(await res.blob());
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(API_BASE + path, {
      method,
      // The kernel requires JSON on every state-changing request.
      headers: method !== 'GET'
        ? { 'Content-Type': 'application/json', 'X-Seed-Token': CONTROL_TOKEN }
        : { 'X-Seed-Token': CONTROL_TOKEN },
      body: method !== 'GET' ? JSON.stringify(body ?? {}) : undefined,
    });
  } catch {
    reportReachable(false);
    throw new NetworkError();
  }
  // Signed out (here or from another browser), or a dev token went stale:
  // reload, which shows the sign-in page if this browser's session is gone.
  if (res.status === 401 && !sessionStorage.getItem('seed-reloaded')) {
    try { sessionStorage.setItem('seed-reloaded', '1'); } catch { /* ignore */ }
    window.location.reload();
    throw new NetworkError();
  }
  if (res.ok) { try { sessionStorage.removeItem('seed-reloaded'); } catch { /* ignore */ } }
  // A dev proxy (or a reverse proxy) answers with 502/503/504 when the kernel is down.
  if (res.status === 502 || res.status === 503 || res.status === 504) {
    const ct = res.headers.get('content-type') || '';
    if (!ct.includes('application/json')) {
      reportReachable(false);
      throw new NetworkError();
    }
  }
  reportReachable(true);
  const text = await res.text();
  let json: unknown = undefined;
  if (text) {
    try {
      json = JSON.parse(text);
    } catch {
      if (!res.ok) throw new ApiError(res.status, text.slice(0, 200) || res.statusText);
      throw new ApiError(res.status, 'Invalid JSON from kernel');
    }
  }
  if (!res.ok) {
    const err = json && typeof json === 'object' ? (json as { error?: unknown }).error : undefined;
    throw new ApiError(res.status, err ? String(err) : res.statusText || `HTTP ${res.status}`);
  }
  return json as T;
}

const enc = encodeURIComponent;

export const api = {
  status: () => request<Status>('GET', '/status'),
  messages: (limit = 200) => request<Message[]>('GET', `/messages?limit=${limit}`),
  sendMessage: (content: string, drafts?: string[]) => request<Message>('POST', '/messages', drafts?.length ? { content, drafts } : { content }),
  evolutions: () => request<Evolution[]>('GET', '/evolutions'),
  createEvolution: (intent: string) => request<Evolution>('POST', '/evolutions', { intent }),
  evolution: (id: string) => request<{ evolution: Evolution; events: EvolutionEvent[] }>('GET', `/evolutions/${enc(id)}`),
  evolutionDiff: (id: string) => request<Diff>('GET', `/evolutions/${enc(id)}/diff`),
  cancelEvolution: (id: string) => request<Evolution>('POST', `/evolutions/${enc(id)}/cancel`),
  /** Answer the questions an evolution is waiting on (stored as the owner's message). */
  answer: (id: string, answer: string) => request<Message>('POST', `/evolutions/${enc(id)}/answer`, { answer }),
  approvals: () => request<Approval[]>('GET', '/approvals'),
  decide: (id: string, approved: boolean) => request<Approval>('POST', `/approvals/${enc(id)}`, { approved }),
  generations: () => request<Generation[]>('GET', '/generations'),
  rollback: (n: number, withData = false) => request<Evolution>('POST', `/generations/${n}/rollback`, withData ? { with_data: true } : undefined),
  notifications: () => request<NotificationsInfo>('GET', '/notifications'),
  subscribe: (s: { endpoint: string; p256dh: string; auth: string }) => request<PushDevice>('POST', '/notifications/subscriptions', s),
  unsubscribe: (id: string) => request<NotificationsInfo>('POST', `/notifications/subscriptions/${enc(id)}/remove`),
  setNotifyKinds: (kinds: Record<string, boolean>) => request<NotificationsInfo>('POST', '/notifications/kinds', { kinds }),
  testNotification: () => request<{ delivered: number }>('POST', '/notifications/test'),
  backups: () => request<BackupsInfo>('GET', '/backups'),
  takeBackup: () => request<Backup>('POST', '/backups'),
  restoreBackup: (id: string) => request<{ state: string }>('POST', `/backups/${enc(id)}/restore`),
  setDailyBackups: (daily: boolean) => request<BackupsInfo>('POST', '/backups/settings', { daily }),
  skills: () => request<Skill[]>('GET', '/skills'),
  skill: (name: string) => request<Skill>('GET', `/skills/${enc(name)}`),
  knowledge: () => request<Knowledge>('GET', '/knowledge'),
  knowledgeFile: (path: string) => request<KnowledgeFile>('GET', `/knowledge/file?path=${enc(path)}`),
  logs: (source: LogSource, tail = 500) => request<Logs>('GET', `/logs?source=${source}&tail=${tail}`),
  settings: () => request<Settings>('GET', '/settings'),
  extensions: () => request<Extension[]>('GET', '/extensions'),
  model: () => request<ModelConfig>('GET', '/model'),
  modelOptions: (provider: string) => request<ModelOptions>('GET', `/model/options?provider=${enc(provider)}`),
  setModel: (body: SetModelBody) => request<ModelConfig>('POST', '/model', body),
  forgetKey: (provider: string) => request<ModelConfig>('POST', '/model/forget-key', { provider }),
  decideEvolution: (id: string, action: 'apply' | 'changes' | 'discard', feedback?: string) =>
    request<{ ok: boolean }>('POST', `/evolutions/${enc(id)}/decide`, { action, feedback }),
  evolutionSettings: () => request<{ preview: boolean }>('GET', '/evolution-settings'),
  setEvolutionSettings: (preview: boolean) => request<{ preview: boolean }>('POST', '/evolution-settings', { preview }),
  incidents: () => request<Incident[]>('GET', '/incidents'),
  fixIncident: (id: string) => request<Evolution>('POST', `/incidents/${enc(id)}/fix`),
  ignoreIncident: (id: string) => request<{ ok: boolean }>('POST', `/incidents/${enc(id)}/ignore`),
  diagnoseIncident: (id: string) => request<{ ok: boolean }>('POST', `/incidents/${enc(id)}/diagnose`),
  healthSettings: () => request<{ auto_fix: boolean }>('GET', '/health/settings'),
  setHealthSettings: (auto_fix: boolean) => request<{ auto_fix: boolean }>('POST', '/health/settings', { auto_fix }),
  kernel: () => request<KernelStatus>('GET', '/kernel'),
  checkKernel: () => request<KernelStatus>('POST', '/kernel/check'),
  updateKernel: (force = false) => request<{ to: string; generation: number }>('POST', '/kernel/update', { force }),
  routines: () => request<Routine[]>('GET', '/routines'),
  spending: () => request<Spending>('GET', '/spending'),
  setBudget: (budget_usd: number) => request<Spending>('POST', '/spending/budget', { budget_usd }),
  createRoutine: (body: NewRoutine) => request<Routine>('POST', '/routines', body),
  updateRoutine: (id: string, body: Partial<NewRoutine> & { enabled?: boolean }) => request<Routine>('POST', `/routines/${enc(id)}/update`, body),
  runRoutine: (id: string) => request<RoutineRun>('POST', `/routines/${enc(id)}/run`),
  deleteRoutine: (id: string) => request<{ ok: boolean }>('POST', `/routines/${enc(id)}/delete`),
  routineRuns: (id: string) => request<RoutineRun[]>('GET', `/routines/${enc(id)}/runs`),
  outbound: () => request<Outbound>('GET', '/outbound'),
  grantOutbound: (body: { hosts?: string[]; secrets?: string[]; reason?: string }) => request<Outbound>('POST', '/outbound/grant', body),
  revokeOutbound: (kind: 'host' | 'secret', value: string) => request<Outbound>('POST', '/outbound/revoke', { kind, value }),
  ownerSessions: () => request<OwnerSession[]>('GET', '/owner/sessions'),
  revokeSession: (id: string) => request<{ ok: boolean }>('POST', `/owner/sessions/${enc(id)}/revoke`),
  badgeSetting: () => request<{ enabled: boolean }>('GET', '/owner/badge'),
  setBadge: (enabled: boolean) => request<{ enabled: boolean }>('POST', '/owner/badge', { enabled }),
  logout: () => request<{ ok: boolean }>('POST', '/owner/logout'),
  loginLink: () => request<{ path: string; expires_at: string }>('POST', '/login-links'),
};

// ---- helpers shared by views ----

export const TERMINAL_STATUSES: EvolutionStatus[] = ['complete', 'failed', 'cancelled', 'rolled_back'];
export const isActive = (s: EvolutionStatus) => !TERMINAL_STATUSES.includes(s);
/** The Seed is waiting for its owner to answer questions on this evolution. */
export const isWaitingOnOwner = (e: Evolution | null | undefined) => !!e && e.status === 'needs_input' && !!e.questions?.length;
export const shortCommit = (c?: string) => (c ? c.slice(0, 7) : '');
export const errorMessage = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** An evolution's name for people: its plan's title once there is one;
 *  before that, the first line of what it was asked (without the owner's
 *  words or evidence the kernel adds for the agent). */
export function evolutionTitle(e: { title?: string; intent: string; plan?: { title?: string } | null }): string {
  if (e.plan?.title) return e.plan.title;
  if (e.title) return e.title;
  const first = e.intent.split(/\n\s*\n|\n+The owner's exact words:/)[0].trim();
  return first.length > 140 ? first.slice(0, 140).trimEnd() + '…' : first;
}
