// Typed client for the Seed kernel control-plane API (docs/api.md).

export const API_BASE = '/_seed/api';

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
};

export type Message = {
  id: string;
  conversation_id: string;
  role: 'user' | 'seed' | 'system';
  content: string;
  evolution_id?: string;
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
};

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
  checks: Check[];
  reflection: Reflection | null;
  summary?: string;
  error?: string;
  kind: 'evolve' | 'rollback';
  created_at: string;
  updated_at: string;
  completed_at?: string;
};

export type EvolutionEventKind =
  | 'phase' | 'plan' | 'tool_call' | 'tool_result' | 'check' | 'note' | 'error' | 'approval' | 'agent_text';

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

// ---- SSE event payloads ----
export type LiveEventMap = {
  message: Message;
  evolution: Evolution;
  evolution_event: EvolutionEvent;
  approval: Approval;
  status: Status;
  chat: { thinking: boolean };
};
export type LiveEventName = keyof LiveEventMap;
export type LiveEvent = { [K in LiveEventName]: { type: K; data: LiveEventMap[K] } }[LiveEventName];
export const LIVE_EVENT_NAMES: LiveEventName[] = ['message', 'evolution', 'evolution_event', 'approval', 'status', 'chat'];

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

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(API_BASE + path, {
      method,
      // The kernel requires JSON on every state-changing request.
      headers: method !== 'GET' ? { 'Content-Type': 'application/json' } : undefined,
      body: method !== 'GET' ? JSON.stringify(body ?? {}) : undefined,
    });
  } catch {
    reportReachable(false);
    throw new NetworkError();
  }
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
  sendMessage: (content: string) => request<Message>('POST', '/messages', { content }),
  evolutions: () => request<Evolution[]>('GET', '/evolutions'),
  createEvolution: (intent: string) => request<Evolution>('POST', '/evolutions', { intent }),
  evolution: (id: string) => request<{ evolution: Evolution; events: EvolutionEvent[] }>('GET', `/evolutions/${enc(id)}`),
  evolutionDiff: (id: string) => request<Diff>('GET', `/evolutions/${enc(id)}/diff`),
  cancelEvolution: (id: string) => request<Evolution>('POST', `/evolutions/${enc(id)}/cancel`),
  approvals: () => request<Approval[]>('GET', '/approvals'),
  decide: (id: string, approved: boolean) => request<Approval>('POST', `/approvals/${enc(id)}`, { approved }),
  generations: () => request<Generation[]>('GET', '/generations'),
  rollback: (n: number) => request<Evolution>('POST', `/generations/${n}/rollback`),
  skills: () => request<Skill[]>('GET', '/skills'),
  skill: (name: string) => request<Skill>('GET', `/skills/${enc(name)}`),
  knowledge: () => request<Knowledge>('GET', '/knowledge'),
  knowledgeFile: (path: string) => request<KnowledgeFile>('GET', `/knowledge/file?path=${enc(path)}`),
  logs: (source: LogSource, tail = 500) => request<Logs>('GET', `/logs?source=${source}&tail=${tail}`),
  settings: () => request<Settings>('GET', '/settings'),
  extensions: () => request<Extension[]>('GET', '/extensions'),
};

// ---- helpers shared by views ----

export const TERMINAL_STATUSES: EvolutionStatus[] = ['complete', 'failed', 'cancelled', 'rolled_back'];
export const isActive = (s: EvolutionStatus) => !TERMINAL_STATUSES.includes(s);
export const shortCommit = (c?: string) => (c ? c.slice(0, 7) : '');
export const errorMessage = (e: unknown) => (e instanceof Error ? e.message : String(e));
