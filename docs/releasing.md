# Releasing

How a version of Seed reaches people: a signed kernel that Seeds update to
from their control plane, the `seed` CLI for `install.sh`, and the hosting
image `ghcr.io/paezao/seed`.

Versions are dates: `2026.10.20`, then `2026.10.20.2` for a second release
the same day.

## Cutting a release

1. **Run it.** GitHub → Actions → **release** → *Run workflow* on `main`.
   Leave the version empty for today's date; notes are optional (empty: the
   commits since the last release). Pushing a tag works too:
   `git tag -a v2026.10.20 -m "notes" && git push origin v2026.10.20`.
2. **Checks.** The same checks as every push (build, lint, all tests in the
   runtime image) plus the **boot smoke test** (`scripts/smoke.sh`: plant a
   Seed with the new CLI, start it, check it answers).
3. **Approve.** Signing waits in the protected **release** environment for
   your approval (Actions → the run → *Review deployments*). The signing key
   lives only there.
4. **Review the draft.** The workflow tags the version, builds and signs the
   kernel, builds the CLI for Linux and macOS (amd64, arm64), attests where
   every file came from, and makes a **draft** release. Seeds don't see
   drafts. Edit the notes: they're what Seeds show in their update banner.
5. **Publish.** It becomes the latest release: `install.sh` installs it,
   Seeds offer it within 6 hours, and the **image** workflow builds
   `ghcr.io/paezao/seed:<version>` and `:latest` for amd64 and arm64, signs it
   with cosign (keyless) and attests it.

Check locally before a release with `make test` and `make smoke`.

## What protects it

- Seeds install only kernels signed by a key they carry
  (`kernel/update/keys.go`), only newer than their own, and roll back on
  their own if one fails to start ([kernel updates](updates.md)).
- The signing key is a secret of the **release** environment, which only
  `main` and `v*` tags can use and which needs approval.
- Release files carry build provenance:
  `gh attestation verify seed_linux_amd64.tar.gz --repo paezao/seed`. The image
  is signed: `cosign verify ghcr.io/paezao/seed:<version>
  --certificate-identity-regexp 'https://github.com/paezao/seed/' --certificate-oidc-issuer https://token.actions.githubusercontent.com`.
- `install.sh` checks the downloaded archive against `checksums.txt`.
- Every GitHub Action is pinned to a commit; Dependabot proposes updates.

## When a release is bad

Seeds never downgrade, so the fix is a newer release: fix it on `main` and
release again (same day: `2026.10.20.2`). Seeds whose new kernel failed to
start have already rolled back on their own. Mark the bad release in its
notes on GitHub; leave it published, so the newer one stays "latest".

## The signing key

Kept in the release environment's `SEED_RELEASE_KEY` secret, and offline by
the maintainer (losing it means Seeds can't verify new releases). Made with
`seed release keygen --out release.key`, which prints the public key for
`kernel/update/keys.go`.

To rotate: add the new public key to `keys.go`, release (signed with the old
key), then switch the secret to the new key. Remove the old public key in a
later release.
