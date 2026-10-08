# Contributing to Seed

Thanks for helping a Seed grow. Bug reports, ideas, docs and code are all
welcome.

## Before you start

- For bugs and ideas, open an issue (templates help you say what's useful).
- For anything security-related, see [SECURITY.md](SECURITY.md): please don't
  open a public issue.
- For a larger change, open an issue first so we can agree on the approach.

## Building and testing

You need Go (see go.mod), Node 24, Docker and make. Then:

```bash
make build     # control plane, template, and bin/seed
make test      # all Go tests, in the runtime image (bubblewrap, PostgreSQL)
make lint      # go vet and friends
```

`make test` runs inside the same container a Seed lives in, so sandboxing and
the database behave as they do for real. The end-to-end test (`e2e/`) grows a
real Seed with a real model and needs a paid OpenRouter key
(`SEED_TEST_OPENROUTER_API_KEY`); it isn't run on pull requests.

[docs/development.md](docs/development.md) covers running a Seed from source,
the dev server for the control plane, and how the pieces fit.

## Pull requests

- Keep them focused: one change, with tests for what it changes.
- Match the surrounding code: names, comment density, and the Seed's voice in
  anything it says to its owner ("I"), plain and specific.
- Security-sensitive code (sandbox, secrets, sign-in, the proxy, updates) gets
  a careful review: say what you thought about.
- If you change `Dockerfile` or `deploy/*.Dockerfile`, run
  `make deploy-dockerfile`.

## License

Seed is licensed under the [Mozilla Public License 2.0](LICENSE). By
contributing, you agree that your contributions are licensed under it. The
starter app in `organism/` is MIT (see [organism/LICENSE](organism/LICENSE)).
