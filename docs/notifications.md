# Notifications

A Seed tells its owner's browsers when something needs them, or something
they'd want to know happened, even when no control-plane tab is open. It
uses standard Web Push: no account, no third-party service of its own.

## Turning them on

**Settings → Notifications → Turn on in this browser.** The browser asks for
permission, subscribes, and the Seed remembers the subscription. Do it in
each browser you want notified (phone included). *Send a test* checks it
works; the list shows each browser, when it was last notified, and whether
the last one failed.

On iPhone and iPad, Safari only delivers Web Push to sites added to the Home
Screen.

The control plane must be served over https (or from `localhost`): browsers
only allow push there.

## What they're about

Each kind can be switched off:

| | |
|---|---|
| **Needs you** | a change is ready to try, the Seed has a question, or something waits for approval |
| **Changes** | a new generation went live, or a change failed |
| **Health** | the Seed found a problem in its app (a diagnosed incident) |
| **Budget** | this month's budget is spent |
| **Backups** | a restore finished or failed |
| **Updates** | a new kernel is available |
| **Routine reports** | a routine has something to say (off by default) |

Each notification is sent once per change of state (an evolution that is
published many times while it waits notifies once), and clicking it opens
the control-plane page it's about.

## How it works

- The kernel watches its own event bus, so nothing else has to remember to
  notify.
- Messages are encrypted for each browser (RFC 8291) and signed with the
  Seed's VAPID key (RFC 8292), made on first start and kept in its memory.
  The push service (Google's, Mozilla's, Apple's or Microsoft's) only sees
  ciphertext.
- The service worker at `/_seed/sw.js` (scope `/_seed/`) only shows
  notifications; it never handles requests, and a click only opens pages
  under `/_seed/`.
- The kernel only posts to the push services browsers use
  (`fcm.googleapis.com`, `updates.push.services.mozilla.com`,
  `*.push.apple.com`, `*.notify.windows.com`), over https: a subscription is
  a URL a browser hands over, and nothing else is accepted.
- A subscription the push service says is gone (404/410) is removed.

## API

| | | |
|---|---|---|
| GET | `/notifications` | `{public_key, subscriptions, kinds, about}` |
| POST | `/notifications/subscriptions` `{endpoint, p256dh, auth}` | add this browser |
| POST | `/notifications/subscriptions/:id/remove` | remove a browser |
| POST | `/notifications/kinds` `{kinds}` | switch kinds on or off |
| POST | `/notifications/test` | send a test to every browser |
