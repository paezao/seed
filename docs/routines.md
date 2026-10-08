# Routines

Routines are what a Seed does on its own, on a schedule. There are two kinds.

## Agent routines ("ask me to…")

A task the Seed runs with its chat tools, unattended, for example "every morning at 8, tell me
how many recipes were added yesterday". The Seed reads data, calls its app, and writes a short
report that is posted in the chat. When there is nothing worth saying it replies
`NOTHING_TO_REPORT`, and the run is recorded as *quiet* with no message.

- **Tools:** the same as in chat (`query_data`, `describe_data`, `call_api`, reading files and
  skills). Anything dangerous (changing data, a non-GET call, outbound access) still waits for
  the owner's approval card, for up to 30 minutes per run.
- **Limits:** a routine cannot start evolutions or create routines. At most 20 agent routines,
  each running at most every 15 minutes, one at a time.
- **Creating them:** in the control plane (**Routines → New routine**), or by asking in chat. The
  chat creates one only after the owner approves the exact name, schedule and task on a card. An
  approval is never reused.
- The chat sees past reports as records written while the owner was away, not as its own words.

## Jobs ("call my app")

A request to the organism's own endpoint on a schedule. No model is involved: the app's code
does the work (sending reminders, cleaning up, syncing).

The organism declares its jobs in `organism/routines.yaml`:

```yaml
jobs:
  - name: send-reminders           # kebab-case, unique
    schedule: every 1h             # or cron: "0 8 * * *"
    timezone: Europe/Lisbon        # optional, default UTC
    method: POST                   # optional, default POST
    path: /internal/jobs/send-reminders
    description: Email owners whose recipes have no photo.
```

- Evolutions validate the file (the **routines** check). When a generation goes live, the kernel
  syncs the jobs: new ones are scheduled, changed ones rescheduled, removed ones deleted. If the
  owner paused a job, it stays paused. An invalid file never wipes the existing jobs.
- The kernel calls the endpoint with `X-Seed-Job: <token>`. The live process has the same token
  in `SEED_JOB_TOKEN`, and the endpoint must refuse calls without it. The kernel strips
  `X-Seed-Job` from visitors' requests. A 2xx status is success. The response (first 4 KB) is
  kept with the run, and each call times out after 5 minutes.
- The owner can also create a job by hand in the control plane. Jobs declared by the organism are
  changed by evolving (they are part of its code). The owner can pause them or run them now.

## Schedules

`every 15m`, `every 2h`, `every 1d` (at midnight) or 5-field cron (`minute hour day month
weekday`, with `*`, lists, ranges and steps), in an IANA timezone. Intervals are aligned
(`every 1h` runs on the hour). A routine missed while the Seed was stopped runs once when it
starts again, not once per missed time.

## Runs

Every run is recorded with its trigger (schedule or manual), status (`ok`, `quiet`, `failed`,
`running`) and output; the last 100 runs of each routine are kept. A run interrupted by a restart
is marked failed.
