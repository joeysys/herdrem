// sw-notify.js — imported by the generated service worker (vite.config.ts
// workbox.importScripts). Tapping a blocked/done notification brings the app
// forward: focus an open window if there is one, otherwise open the app.
self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((wins) => {
      const win = wins.find((w) => 'focus' in w)
      if (win) return win.focus()
      return self.clients.openWindow(self.registration.scope)
    }),
  )
})
