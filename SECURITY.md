# Security

Seed runs code that a language model writes, so isolation is its core job.
[docs/security.md](docs/security.md) describes the threat model and every
boundary: the container, the sandboxes, the private network, owner sign-in,
outbound access, signed kernel releases, and the known limits.

## Reporting a vulnerability

Please report privately, not in a public issue:

- **Preferred:** GitHub's private vulnerability reporting (the repository's
  **Security** tab → **Report a vulnerability**).
- Or email **paezao@gmail.com** with "Seed security" in the subject.

Include what you found, how to reproduce it, and what it lets someone do. You'll
get an answer within a few days. Fixes ship as a signed kernel release (Seeds
offer it in their control plane), and you'll be credited unless you'd rather
not be.

## Scope

In scope: the kernel and CLI, the control plane, the runtime and deploy images,
and the release pipeline, in particular anything that lets code a Seed writes
(or anyone on the network) reach beyond its sandbox, read secrets or the
owner's session, act as the owner, or install an unsigned or older kernel.

Out of scope: apps people build with Seed (their organisms), and the limits
already documented in docs/security.md.

## Supported versions

The latest kernel release. Seeds update themselves from the control plane.
