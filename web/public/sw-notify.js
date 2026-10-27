// sw-notify.js — imported by the generated service worker (vite.config.ts
// workbox.importScripts). A Web Push payload is only a pane id and one
// status sentence. Tapping it focuses the app and opens that pane.
self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = {}
  }
  const title = typeof data.title === 'string' && data.title ? data.title : 'herdrem'
  const body = typeof data.body === 'string' ? data.body : ''
  const paneId = typeof data.paneId === 'string' ? data.paneId : ''
  const tag = typeof data.tag === 'string' && data.tag ? data.tag : (paneId || 'herdrem')
  event.waitUntil(self.registration.showNotification(title, {
    body,
    tag,
    renotify: true,
    data: { paneId },
  }))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const paneId = event.notification.data && event.notification.data.paneId
  const target = paneId
    ? new URL('?pane=' + encodeURIComponent(paneId), self.registration.scope).href
    : self.registration.scope
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((wins) => {
      const win = wins.find((w) => 'focus' in w)
      if (win) {
        if (paneId) win.postMessage({ type: 'webpush', paneId })
        return win.focus()
      }
      return self.clients.openWindow(target)
    }),
  )
})
