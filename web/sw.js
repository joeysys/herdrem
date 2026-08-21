/* Herdr service worker — required for Chrome installability */
const CACHE = 'herdr-v12';
const PRECACHE = [
  './',
  './index.html',
  './manifest.webmanifest',
  './icons/sheep.svg',
  './icons/sheep-mark.svg',
  './icons/favicon.ico',
  './icons/favicon-32.png',
  './icons/icon-192.png',
  './icons/icon-512.png',
  './icons/icon-maskable-512.png',
  './icons/apple-touch-icon.png',
  './icons/agents/codex.svg',
  './icons/agents/claude.svg',
  './icons/agents/grok.svg',
  './icons/agents/devin.svg',
  './icons/agents/kiro.svg',
  './icons/agents/copilot.svg',
  './icons/agents/pi.svg',
  './icons/agents/gemini.svg',
  './icons/agents/cursor.svg',
  './icons/agents/opencode.svg',
  './icons/agents/agy.svg',
  './icons/agents/default.svg',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE);
      await Promise.all(
        PRECACHE.map(async (url) => {
          try {
            await cache.add(url);
          } catch (_) {
            /* ignore individual failures */
          }
        })
      );
      await self.skipWaiting();
    })()
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys();
      await Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)));
      await self.clients.claim();
    })()
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  let url;
  try {
    url = new URL(req.url);
  } catch (_) {
    return;
  }

  if (url.origin !== self.location.origin) return;

  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req)
        .then((res) => {
          const copy = res.clone();
          caches.open(CACHE).then((c) => c.put('./index.html', copy)).catch(() => {});
          return res;
        })
        .catch(async () => {
          const cached = (await caches.match(req)) || (await caches.match('./index.html'));
          return cached || new Response('Offline', { status: 503, statusText: 'Offline' });
        })
    );
    return;
  }

  // Manifest: network-first so orientation/icon changes propagate without
  // waiting for a full SW update cycle.
  if (url.pathname === '/manifest.webmanifest' || url.pathname.endsWith('/manifest.webmanifest')) {
    event.respondWith(
      fetch(req)
        .then((res) => {
          const copy = res.clone();
          caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {});
          return res;
        })
        .catch(async () => {
          const cached = await caches.match(req);
          return cached || new Response('{}', { headers: { 'Content-Type': 'application/manifest+json' } });
        })
    );
    return;
  }

  event.respondWith(
    caches.match(req).then((hit) => {
      if (hit) return hit;
      return fetch(req).then((res) => {
        if (res && res.ok && res.type === 'basic') {
          const copy = res.clone();
          caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {});
        }
        return res;
      });
    })
  );
});
