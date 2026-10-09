# Security

A Seed executes code that it wrote itself, and runs shell commands that a model chose. The
design assumes autonomy will grow, so trust boundaries are structural rather than advisory.

## Sandboxing

A Seed runs in **one container**, an unprivileged container running as your user with all
capabilities dropped, `no-new-privileges`, a **read-only root filesystem**, and memory, CPU and
process limits (`SEED_MEMORY`, `SEED_CPUS`, `SEED_PIDS_LIMIT`). Inside it, every command an evolution runs, and the
live organism, is isolated again with **bubblewrap** (`kernel/sandbox/bwrap.go`). Each sandbox
gets:

- an empty root with only system directories mounted read-only (`/usr`, `/etc`, …);
- the workspace at `/workspace`, **read-only** except the evolvable directories;
- empty tmpfs over `.seed/`, over the live Seed's folder and over the owner's config (API keys);
- its own PID, IPC and UTS namespaces, a private `/tmp`, and a clean environment (no kernel env vars);
- the Seed's PostgreSQL only through its Unix socket, mounted read-only, with a role that has no
  access to kernel memory;
- timeouts enforced by killing the whole process group.

Nested bubblewrap needs to create namespaces and mount filesystems. Docker's default seccomp
profile allows those calls only with `CAP_SYS_ADMIN`, which a Seed never gets. A Seed therefore
uses a **tailored seccomp profile** (`cmd/seed/seccomp.json`): Docker's default profile with only
`clone`, `unshare`, `setns`, `mount`, `umount2` and `pivot_root` allowed unconditionally. AppArmor's
`docker-default` profile and `/proc` masking also forbid these, so those two are relaxed. There is
still no privileged mode and no capability.

**Network.**
- Evolution sandboxes share the container's network so they can install dependencies.
- The **live organism runs in its own network namespace**. Nothing in the container can reach
  it over TCP; the kernel talks to it through a Unix socket bridge. Experimental code can never
  call the live app.
- The kernel **refuses connections that originate inside its own container**. Sandboxes cannot use
  it, or its proxy to the live app, as a way out; the owner arrives through the published port.

**Build caches.** The kernel is built with its own Go caches (`.seed/kernel-cache`), which no
sandbox can see. Sandboxes have a separate writable cache (`.seed/cache`), so sandboxed code
cannot poison the next kernel build.

The runtime image is built from `Dockerfile` **without a build context**, so it cannot copy
repository files or secrets.

## Permissions

Every tool call is classified before it runs (`tools.Registry.Execute`):

| level | examples | default |
|---|---|---|
| safe | read files, list, search, logs, schema, git status/diff, HTTP to the sandbox | allow |
| review | write organism/knowledge/skills, run commands in the sandbox, migrations on the scratch DB, dependency installs | allow |
| dangerous | writing or deleting any protected (kernel) path | **ask** |

Decisions are `allow`, `ask` or `deny` per level in `seed.yaml`. `ask` creates an approval and
moves the evolution to `needs_input`. The control plane shows it, and the evolution blocks until
the owner decides. Plan and apply approvals can also be required (`require_plan_approval`,
`require_apply_approval`).

## The kernel boundary

The boundary is an **allowlist**: only `organism/`, `knowledge/` and `skills/` evolve freely
(`kernel.evolvable`). Everything else is the kernel. That includes `kernel/`, `cmd/`, `control/`,
`seed.yaml`, `go.mod` and every root file, as well as *new* files such as `go.work` or `vendor/`,
which would change what `go build` compiles on the host. The boundary is guarded in four layers:

1. File tools classify writes outside the evolvable directories as dangerous, which requires
   approval by default.
2. Sandboxes mount the workspace **read-only** except for the evolvable directories, so shell
   commands cannot touch the kernel or create root files.
3. At commit time, the kernel diffs the worktree and refuses any protected change that was not
   approved during that evolution ("kernel boundary violated").
4. `seed run` builds the kernel with `GOWORK=off GOFLAGS=-mod=readonly`.

## Symlinks

Sandboxed code can create symlinks inside evolvable directories. Every host-side read or write of
repository content goes through Go's `os.Root` (`kernel/fsx`, and the file tools), including
knowledge, skills, migrations, the logo and extensions. A link such as
`knowledge/x.md → ~/.ssh/id_rsa` therefore fails instead of leaking a host file into a prompt.

Kernel evolution is therefore possible but deliberate. After an approved kernel change is applied,
the kernel restarts into its own new source.

## Data

- Each Seed's PostgreSQL is private to its container. It listens only on a Unix socket, and its
  superuser password is random and stored in `.seed/secrets` (0600, hidden from sandboxes).
- The live organism uses a non-superuser role that owns only its live database. Evolutions use a
  separate role that owns only scratch databases, so an experiment can never touch live data.
  `CONNECT` is revoked from `PUBLIC` on every database.
- **No credentials are stored.** The owner passes secrets (model API key, tokens) at every start
  (`seed run -e KEY`, `--env-file`), or the platform sets them when deployed. The kernel keeps them
  in memory and removes them from its environment at boot, so nothing it starts inherits them.
  (A process's *initial* environment stays readable in `/proc` to whoever controls the container,
  i.e. the owner or platform; sandboxes have their own PID namespace and `/proc` and cannot see
  it.) They never reach a repository, the
  Seed's folder or database, logs, model prompts or sandboxes, and the API never returns them. A
  key lent in the control plane lasts for the session only. Provider endpoints are fixed, so a
  caller cannot redirect a key to another host.
- File tools never write through symlinks, so the path that is permission-checked is the path that
  is written. On Linux this is enforced at the moment of each operation (`openat2` with
  `RESOLVE_NO_SYMLINKS|RESOLVE_BENEATH`), with no check-then-write race against sandboxed processes.
- Model API keys come from the environment and are never written to the repository or to
  `seed.yaml`. Settings returned by the API omit the database URL.
- The logo is generated content. It is served with `Content-Security-Policy: sandbox` and
  rendered via `<img>`, so it cannot run script. Markdown in the control plane renders raw HTML as
  text.

## Control plane

The control plane shares an origin with the organism (`/_seed` and `/`). The organism's
JavaScript is code the Seed wrote, so the kernel does not trust it:

- **Owner sign-in.** The control plane is only for its owner. A browser signs in with a username
  and password, or with a one-time link (`seed login`, or opened by `seed run`; one use,
  15 minutes). The password is a secret passed at start (`SEED_OWNER_PASSWORD`, at least 12
  characters; `SEED_OWNER_USER` defaults to `owner`). Without it, only links work. The kernel
  keeps a keyed hash of them in memory and compares in constant time. Failed attempts are
  limited: 5 per client and 1000 overall per 15 minutes. While a client is limited nothing is
  checked, not even a correct password. While the overall limit holds, signed-in browsers and
  `seed login` links still work. A client is its IP address (IPv6: its /48).
  `X-Forwarded-For` is ignored unless `SEED_TRUSTED_PROXIES` says how many proxies are in front
  (1 behind a platform's ingress); otherwise clients could forge it. The form only accepts
  same-origin navigations, and the sign-in page has opener isolation, so an organism page cannot
  open it and read what is typed.
- **Known limit: saved passwords.** Password managers fill saved passwords by origin, and the
  organism shares the control plane's origin, so an organism page could get the saved Seed
  password filled into a form of its own (Firefox fills on page load). Don't let the browser save
  it until the control plane has its own hostname (planned for hosted Seeds). The kernel then sets an
  HttpOnly, `SameSite=Lax` cookie scoped to `/_seed`, valid for 30 days and renewed with use.
  Anyone without it gets a "this Seed is private" page. Sessions are stored as hashes in kernel
  memory, so they survive restarts. They can be signed out from Settings.
- **The cookie is never enough.** Organism scripts share the origin, so their requests to `/_seed`
  would carry the cookie too. The cookie therefore only unlocks the control-plane page. That page
  carries an API token derived from the cookie, and every `/_seed/api` call must present it in a
  header (the logo is the only exception). Signing out invalidates both.
- **CLI token.** The CLI uses a separate random token generated each time the kernel starts and
  written to `.seed/control-token` (hidden from sandboxes). It never reaches a browser.
- **Seeds on one machine.** Browsers share cookies across ports, so each Seed uses its own cookie
  name. Every local Seed's kernel still receives the others' cookies on requests to `/_seed`.
  Kernels are code the owner approved, but it is one more reason a Seed's kernel changes need
  approval.
- **Only for navigations.** The page carrying the token, sign-in links and the sign-in form are
  served only for top-level navigations (`Sec-Fetch-Dest: document`, `Sec-Fetch-Mode: navigate`),
  never to `fetch`/XHR. Browsers that send no Fetch Metadata (Safari before 16.4) are refused.
  The API token travels only in a header, never in a URL. It cannot be framed
  (`frame-ancestors 'none'`), so the Approve button cannot be clickjacked.
- **Opener isolation.** The control plane sends `Cross-Origin-Opener-Policy: same-origin`, and the
  proxy forces `unsafe-none` on organism pages. An organism page that opens `/_seed` in a popup
  therefore cannot read it.
- **Admin screens on their own origin.** Organism admin screens shown inside the control plane are
  framed from `organism.localhost:<port>`, never the control plane's origin. The kernel refuses to
  serve `/_seed` on that origin. Organism pages on the main origin cannot be framed at all
  (`frame-ancestors 'none'`), so a framed screen cannot navigate itself back to the control
  plane's origin. The frame gets no popups.
- **No service workers.** The organism cannot register them, because a worker at `/` would also
  control `/_seed`.
- **Other sites.** Requests for any Host other than localhost, `127.0.0.1`, `[::1]` or `*.localhost` are
  refused (`server.allowed_hosts` adds more), which defeats DNS rebinding. State-changing calls must
  be JSON and must come from the same origin.

- **The owner's badge.** On organism pages loaded as top-level navigations, the kernel adds one
  script (`/_seed/badge.js`). The script asks `/_seed/badge` whether this browser is the
  owner's: the session cookie is sent, and the answer is only yes (200) or no (204), never a
  token. It then shows a small floating link back to `/_seed/` in a closed shadow root: the Seed's current logo (as an image, which cannot run script), or the seed when it has none. The
  organism's own scripts can ask the same question, so **an organism can tell when its owner is
  the one looking**: compromised organism code could behave well only then. That's the price of
  showing something only to the owner on the organism's own page. The badge is on by default and
  can be switched off in Settings. Turned off, `/_seed/badge` answers no to everyone and nothing
  is injected.
  Hovering the badge also offers **point and ask**: the owner drags a box around part of the
  page and says what should change. The badge never sends a message itself (it shares the page
  with the organism's scripts, which could drive it): it opens the control plane with the
  request in the URL fragment (`/_seed/#ask=…`, never sent to a server), and only the owner's
  **Send** there posts it to chat. A page could forge an ask, but the owner sees and edits every
  word before it is sent; from the page it carries only its path (path characters, length-limited).
  The picture of the area is drawn in the browser (with a vendored copy of modern-screenshot
  served by the kernel) and left at `/_seed/ask-draft`: only the owner's browser, same-origin,
  with the badge on, and only a real PNG or JPEG up to 3 MB and 4096 px. A draft does nothing by
  itself; at most 8 are kept, for 30 minutes, and one becomes part of a message only when the
  owner sends the ask with it. The picture is page data: the model is told that text in it isn't
  instructions.
  Pages are rewritten only when uncompressed and under 8 MB. Admin screens (the
  `organism.localhost` origin) and API responses are never touched.

- **No shared cache for processes holding real data.** The build cache (Go modules, npm) is
  shared by every sandbox, including evolutions with open network. Long-running processes in a
  private network (the live organism, previews) don't get it, so it can't carry their data to
  an evolution. Their builds still use it; their own caches go to their private /tmp.

## Outbound access

The live organism runs in its own network namespace with no network. Its only way out is the
kernel's proxy, reached through a socket and exposed inside as `HTTPS_PROXY` (Go and Node honor
it; `NODE_USE_ENV_PROXY=1` is set for Node's `fetch`):
- **HTTPS to approved hosts only.** The proxy allows only `CONNECT` to port 443, for hosts the owner
  approved (`api.example.com`, or `*.example.com` for subdomains). Plain HTTP, other ports and IP
  addresses are refused. Approved hosts cannot be internal names (`localhost`, `.internal`, `.local`
  and so on).
- **Never somewhere private.** The proxy resolves the name itself and refuses it if any address
  is private, loopback, link-local (cloud metadata), CGNAT, documentation or NAT64/6to4. It then
  dials exactly the address it checked, so DNS cannot change the answer in between.
- **The TLS server name must match.** After the tunnel opens, the proxy reads the TLS ClientHello
  and refuses the tunnel unless its server name (SNI) is the allowed host, so a tunnel to an
  allowed host on a shared CDN cannot reach another site there. (Fronting by the HTTP `Host`
  header inside TLS cannot be seen by the proxy; allow hosts you trust.)
- **Asking.** Evolutions and the chat ask with `request_outbound_access` (hosts, secret names and
  a reason). That is a dangerous action: the owner sees exactly the request and approves or
  denies it. Exactly that request is granted. An earlier approval is never reused, so access the
  owner revoked cannot come back without them. Refused hosts are shown (and given to the Seed)
  only as well-formed names. The owner can also grant and revoke in Settings, where refused connections are listed.
  Revoking a host takes effect for new connections at once.
- **Secrets.** Only the live organism process gets the secrets the owner granted, and only if they
  were passed at start. Build, install and migration commands never see them: those run outside
  the private network. Without the bwrap driver (no private network) no secrets are given. The owner's sign-in credentials (`SEED_*`) and the kernel's own variables
  can never be granted. Granting or revoking a secret restarts the organism. Evolution test runs
  never get secrets. A granted secret can be sent anywhere the organism may reach, so grant secrets
  together with the hosts they are meant for.
- Limits: 64 concurrent connections, 5 minutes idle. Evolution sandboxes keep general network
  access (they install dependencies); this applies to the live organism.

## Operating on live data

The chat can read live data with `query_data` or a GET `call_api`. Reads are enforced by the
database, not by the prompt:
- they connect as a dedicated **reader role** (`pg_read_all_data`, CONNECT on the live database
  only), so no SQL whatsoever can write;
- they also run as a **single statement** in a read-only transaction.

Changing live data (`change_data`, or a non-GET `call_api`) is classified dangerous, so the owner
approves each change before it runs. Dangerous requests are shown to the owner **in full**, never
truncated, each field rendered readably. Requests too large to review (over 16 KB) are refused.

## Known limitations (v0.1)

- The live organism has no outbound network access (it runs in a private network namespace).
  An egress proxy with an allowlist is the planned way to give apps controlled outbound access.

- **Same origin.** The mitigations above are layered, but a separate origin for the control plane
  (for example `seed.localhost:8080`) would be stronger still. It is a candidate for v0.2.
- The control plane has no authentication. It binds to 127.0.0.1 by default; do not expose it.
- Sandboxes have outbound network access.
- Rollback does not reverse database migrations.
