# Kernel updates

A Seed builds its kernel from its own source, so a new kernel arrives as a
change to that source: a new generation. There are two ways in:

- **From the control plane.** The Seed checks for releases every 6 hours (and
  on **Settings → Kernel → Check now**). When a newer one exists, a banner
  shows its notes and an **Update** button. Updating installs it as a
  generation (the same logic as `seed upgrade`) and restarts the Seed into it.
- **From the CLI:** `seed stop && seed upgrade && seed run` gives a stopped
  Seed the kernel of your `seed` CLI.

## Releases are signed

A release is `template.tar.gz` (the pristine Seed), `manifest.json` (version,
date, notes, the template's SHA-256, the runtime Dockerfile's SHA-256) and
`manifest.json.sig` (Ed25519). A Seed installs a release only if:

- the manifest's signature verifies with a key in `kernel/update/keys.go`;
- the downloaded template matches the manifest's hash;
- the template carries the manifest's version and date (`kernel/VERSION`,
  `kernel/RELEASED`, stamped by `make template`);
- it is newer than the kernel the Seed is running, judged by that kernel's own
  `kernel/RELEASED` (so it holds however the kernel got there: an update,
  `seed upgrade`, planting or a rollback). An older signed release can't be
  replayed to downgrade a Seed. It also must not be a version that already
  failed to start here;
- its runtime image is the Seed's current one. Otherwise the banner says to
  restart with `seed upgrade`, or to redeploy the new image.

Nothing is installed while an evolution is running. If the Seed changed its
own kernel (an approved kernel evolution), the update stops and lists those
files. The owner can choose to replace them.

`SEED_RELEASES_URL` points a Seed at a mirror (releases must still be signed
by a key it carries). `SEED_UPDATES=off` turns checking off.

## If a new kernel fails

After a kernel has run for 30 seconds, it records its commit
(`.seed/kernel-good`) and keeps a copy of its binary (`.seed/bin/seed.good`).
If a new kernel fails to build, or exits with an error within a minute of
starting, the boot script runs that last good binary:
`seed kernel-rollback`. It restores **only the kernel's files** from the last
good commit, keeping everything the Seed did since, and commits that as a new
generation. On the next start the Seed tells its owner what happened, with the
build log. If the kernel files are already the good ones, nothing is rolled
back (the problem is elsewhere, e.g. the database) and the container exits as
before.

## Publishing a release

1. Once: `seed release keygen --out ~/seed-release.key` prints the public key
   for `kernel/update/keys.go`; store the private key as the repository secret
   `SEED_RELEASE_KEY` (and somewhere safe).
2. Tag with the notes as the message, and push:
   `git tag -a v2026.10.20 -m "Faster evolutions. …" && git push origin v2026.10.20`.
3. `.github/workflows/release.yml` builds the template, signs it and publishes
   the GitHub release that Seeds find at `releases/latest`.

To rotate the key, ship a release that adds the new public key, then sign
with the new key from the next release on.
