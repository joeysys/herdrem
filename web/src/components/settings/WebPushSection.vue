<script setup lang="ts">
import { onMounted, ref } from 'vue'

interface PushStatus {
  enabled: boolean
  subscribed?: boolean
  stable: boolean
  reason?: string
  publicKey?: string
  input?: boolean
  done?: 'off' | 'long' | 'always'
}

const available = ref(false)
const loaded = ref(false)
const status = ref<PushStatus | null>(null)
const busy = ref(false)
const message = ref('')
const inputOn = ref(true)
const doneMode = ref<'off' | 'long' | 'always'>('long')

function bytesToB64Url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf)
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function urlB64ToBytes(value: string): Uint8Array<ArrayBuffer> {
  const padded = value + '='.repeat((4 - (value.length % 4)) % 4)
  const raw = atob(padded.replace(/-/g, '+').replace(/_/g, '/'))
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

function applyPrefs(body: PushStatus) {
  if (typeof body.input === 'boolean') inputOn.value = body.input
  if (body.done === 'off' || body.done === 'long' || body.done === 'always') doneMode.value = body.done
}

async function currentEndpoint(): Promise<string> {
  if (!('serviceWorker' in navigator)) return ''
  const reg = await navigator.serviceWorker.getRegistration()
  const sub = await reg?.pushManager.getSubscription()
  return sub?.endpoint ?? ''
}

async function load() {
  try {
    const headers: Record<string, string> = {}
    const endpoint = await currentEndpoint()
    if (endpoint) headers['X-WebPush-Endpoint'] = endpoint
    const res = await fetch('/webpush/status', { credentials: 'same-origin', headers })
    if (!res.ok) return
    const body = await res.json() as PushStatus
    status.value = body
    applyPrefs(body)
    available.value = true
  } catch {
    available.value = false
  } finally {
    loaded.value = true
  }
}

async function subscribeBrowser(publicKey: string): Promise<boolean> {
  try {
    if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
      message.value = 'this browser does not support push'
      return false
    }
    const perm = await Notification.requestPermission()
    if (perm !== 'granted') {
      message.value = 'notification permission was not granted'
      return false
    }
    const reg = await navigator.serviceWorker.ready
    const sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlB64ToBytes(publicKey),
    })
    const p256 = sub.getKey('p256dh')
    const auth = sub.getKey('auth')
    if (!p256 || !auth) {
      message.value = 'the browser did not return push keys'
      try { await sub.unsubscribe() } catch { /* already gone */ }
      return false
    }
    const res = await fetch('/webpush/subscribe', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        endpoint: sub.endpoint,
        p256dh: bytesToB64Url(p256),
        auth: bytesToB64Url(auth),
        input: inputOn.value,
        done: doneMode.value,
      }),
    })
    if (!res.ok) {
      message.value = 'could not save the subscription'
      try { await sub.unsubscribe() } catch { /* already gone */ }
      return false
    }
    return true
  } catch {
    message.value = 'could not subscribe this browser'
    return false
  }
}

async function unsubscribeBrowser(): Promise<boolean> {
  if (!('serviceWorker' in navigator)) return true
  const reg = await navigator.serviceWorker.getRegistration()
  const sub = await reg?.pushManager.getSubscription()
  const endpoint = sub?.endpoint ?? ''
  if (!endpoint) return true
  const res = await fetch('/webpush/unsubscribe', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ endpoint }),
  })
  if (!res.ok && res.status !== 404) {
    message.value = 'could not unsubscribe this browser'
    return false
  }
  try {
    await sub?.unsubscribe()
  } catch {
    message.value = 'could not unsubscribe this browser'
    return false
  }
  return true
}

async function setEnabled(on: boolean) {
  if (busy.value) return
  busy.value = true
  message.value = ''
  try {
    if (!on) await unsubscribeBrowser()
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (on) {
      const endpoint = await currentEndpoint()
      if (endpoint) headers['X-WebPush-Endpoint'] = endpoint
    }
    const res = await fetch('/webpush/enabled', {
      method: 'POST',
      credentials: 'same-origin',
      headers,
      body: JSON.stringify({ enabled: on }),
    })
    const body = await res.json().catch(() => ({})) as PushStatus & { error?: string }
    if (!res.ok) {
      message.value = body.reason || body.error || 'could not update push'
      return
    }
    status.value = body
    applyPrefs(body)
    if (on && body.publicKey) {
      const ok = await subscribeBrowser(body.publicKey)
      if (!ok) {
        const why = message.value
        await fetch('/webpush/enabled', {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled: false }),
        })
        status.value = { ...body, enabled: false, subscribed: false }
        message.value = why || 'could not subscribe this browser'
        return
      }
    }
    await load()
  } finally {
    busy.value = false
  }
}

async function subscribeThis() {
  if (busy.value || !status.value?.publicKey) return
  busy.value = true
  message.value = ''
  try {
    const ok = await subscribeBrowser(status.value.publicKey)
    if (ok) await load()
  } finally {
    busy.value = false
  }
}

async function unsubscribeThis() {
  if (busy.value) return
  busy.value = true
  message.value = ''
  try {
    if (await unsubscribeBrowser()) await load()
  } finally {
    busy.value = false
  }
}

async function savePrefs() {
  if (!status.value?.enabled) return
  if (!status.value.subscribed) {
    message.value = 'subscribe this browser first'
    return
  }
  const reg = 'serviceWorker' in navigator ? await navigator.serviceWorker.ready : undefined
  const sub = await reg?.pushManager.getSubscription()
  if (!sub) {
    message.value = 'subscribe this browser first'
    return
  }
  const res = await fetch('/webpush/prefs', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ endpoint: sub.endpoint, input: inputOn.value, done: doneMode.value }),
  })
  message.value = res.ok ? 'preferences saved' : 'could not save preferences'
}

async function sendTest() {
  message.value = ''
  if (!status.value?.subscribed) {
    message.value = 'subscribe this browser before sending a test'
    return
  }
  const res = await fetch('/webpush/test', { method: 'POST', credentials: 'same-origin' })
  message.value = res.ok ? 'test sent' : 'test was not delivered'
}

onMounted(load)
</script>

<template>
  <div v-if="available && status" class="wp-section">
    <h3>push</h3>
    <p v-if="!status.stable" class="wp-hint">{{ status.reason }}</p>
    <template v-else>
      <label class="wp-toggle">
        <span class="wp-text">
          <span class="wp-label">lock-screen alerts</span>
          <span class="wp-hint">relay switch. off by default. each browser subscribes on its own. the push service receives that browser's address, the pane id, and one status sentence. terminal text stays on this relay.</span>
        </span>
        <input
          type="checkbox"
          role="switch"
          class="wp-switch"
          :checked="status.enabled"
          :disabled="busy"
          aria-label="lock-screen alerts"
          @change="setEnabled(($event.target as HTMLInputElement).checked)"
        />
      </label>
      <div v-if="status.enabled" class="wp-prefs">
        <p class="wp-hint" role="status">{{ status.subscribed ? 'this browser is subscribed' : 'this browser is not subscribed' }}</p>
        <button v-if="!status.subscribed" type="button" class="wp-test" :disabled="busy" @click="subscribeThis">subscribe this browser</button>
        <button v-else type="button" class="wp-test" :disabled="busy" @click="unsubscribeThis">unsubscribe this browser</button>
        <template v-if="status.subscribed">
          <label class="wp-check">
            <input v-model="inputOn" type="checkbox" @change="savePrefs" />
            when a pane needs input
          </label>
          <label class="wp-done">
            when a round finishes
            <select v-model="doneMode" aria-label="when a round finishes" @change="savePrefs">
              <option value="off">off</option>
              <option value="long">only after 2 minutes</option>
              <option value="always">always</option>
            </select>
          </label>
          <p class="wp-hint">an alert waits 10 seconds and is dropped if you already handled the pane.</p>
          <button type="button" class="wp-test" @click="sendTest">send test</button>
        </template>
      </div>
      <p v-if="message" class="wp-hint" role="status">{{ message }}</p>
    </template>
  </div>
</template>

<style scoped>
.wp-section { margin-bottom: 26px; }
h3 {
  font-size: var(--text-sm); font-weight: 700; text-transform: uppercase;
  letter-spacing: 0.12em; color: var(--muted); margin-bottom: 10px;
}
.wp-toggle {
  display: flex; align-items: center; justify-content: space-between; gap: 12px;
  cursor: pointer;
}
.wp-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.wp-label { font-size: var(--text-sm); color: var(--text-dim); }
.wp-hint { font-size: var(--text-2xs); color: var(--faint); line-height: 1.4; margin: 8px 0 0; }
.wp-switch {
  appearance: none; flex-shrink: 0; position: relative; margin: 0;
  width: 36px; height: 20px; border-radius: 999px; cursor: pointer;
  background: var(--surface-3, var(--surface-2)); border: 1px solid var(--border-strong);
  transition: background 0.15s, border-color 0.15s;
}
.wp-switch::after {
  content: ''; position: absolute; top: 2px; left: 2px; width: 14px; height: 14px;
  border-radius: 50%; background: var(--muted); transition: transform 0.15s, background 0.15s;
}
.wp-switch:checked { background: var(--green); border-color: var(--green); }
.wp-switch:checked::after { transform: translateX(16px); background: #fff; }
.wp-switch:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }
.wp-prefs { display: flex; flex-direction: column; gap: 8px; margin-top: 12px; }
.wp-check, .wp-done {
  display: flex; align-items: center; justify-content: space-between; gap: 12px;
  font-size: var(--text-sm); color: var(--text-dim);
}
.wp-done select {
  background: var(--surface-2); color: var(--text); border: 1px solid var(--border-strong);
  border-radius: 6px; padding: 4px 8px; font: inherit;
}
.wp-test {
  align-self: flex-start; background: transparent; color: var(--text-dim);
  border: 1px solid var(--border-strong); border-radius: 6px; padding: 4px 10px;
  font: inherit; cursor: pointer;
}
.wp-test:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }
</style>
