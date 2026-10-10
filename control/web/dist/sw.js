// My control plane's service worker. It only shows my notifications (it
// never touches requests), and clicking one opens the page it is about,
// which is always a page of my control plane.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

const str = (v, max) => (typeof v === 'string' ? v.slice(0, max) : '');

self.addEventListener('push', (event) => {
  let m = {};
  try {
    m = event.data ? event.data.json() : {};
  } catch (e) {
    m = {};
  }
  const url = str(m.url, 300).startsWith('/_seed/') ? m.url : '/_seed/';
  const tag = str(m.tag, 100);
  event.waitUntil(self.registration.showNotification(str(m.title, 120) || 'Seed', {
    body: str(m.body, 300),
    tag: tag || undefined,
    renotify: !!tag,
    data: { url },
  }));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = new URL((event.notification.data && event.notification.data.url) || '/_seed/', self.location.origin);
  if (url.origin !== self.location.origin || !url.pathname.startsWith('/_seed/')) return;
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const w of windows) {
      if (new URL(w.url).pathname.startsWith('/_seed/')) {
        await w.focus();
        try {
          await w.navigate(url.href);
        } catch (e) {
          // an uncontrolled page can't be navigated from here; focusing it is enough
        }
        return;
      }
    }
    await self.clients.openWindow(url.href);
  })());
});
