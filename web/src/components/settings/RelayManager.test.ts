import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import RelayManager from './RelayManager.vue'
import { useRelaysStore } from '../../stores/relays'
import { useConnectionStore } from '../../stores/connection'
import { saveSessionToken, sessionTokenFor } from '../../utils/relay'
import { safeStorage } from '../../utils/storage'
import { ws } from '../../api/ws'

vi.mock('../../api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'disconnected',
  },
}))

beforeEach(() => {
  localStorage.clear()
  sessionStorage.clear()
  // safeStorage shadows localStorage with a module-level Map — clearing only
  // localStorage leaves relay metadata and the current-relay pointer leaking
  // between tests
  safeStorage.removeItem('herdrem_relays')
  safeStorage.removeItem('herdrem_relays_current')
  setActivePinia(createPinia())
  vi.mocked(ws.connect).mockClear()
})

describe('RelayManager credentials', () => {
  it('shows the session token in the edit field and can reveal it', async () => {
    useRelaysStore().add('remote', 'wss://remote.example', 'saved-token')
    setActivePinia(createPinia())
    const wrapper = mount(RelayManager)
    await wrapper.findAll('button[aria-label="Edit"]').at(-1)!.trigger('click')

    const field = wrapper.find('input[aria-label="Relay token"]')
    expect((field.element as HTMLInputElement).value).toBe('saved-token')
    expect(field.attributes('type')).toBe('password')
    await wrapper.find('button[aria-label="Show relay token"]').trigger('click')
    expect(field.attributes('type')).toBe('text')
    await wrapper.find('button[aria-label="Hide relay token"]').trigger('click')
    expect(field.attributes('type')).toBe('password')
    wrapper.unmount()
  })

  it('connects immediately when the current relay token is saved from settings', async () => {
    const relays = useRelaysStore()
    const remote = relays.add('remote', 'wss://remote.example')
    relays.setCurrent(remote.id)
    const wrapper = mount(RelayManager)
    await wrapper.findAll('button[aria-label="Edit"]').at(-1)!.trigger('click')
    await wrapper.find('input[aria-label="Relay token"]').setValue('new-token')
    await wrapper.find('.rm-form-actions .primary').trigger('click')

    expect(sessionTokenFor(remote.url)).toBe('new-token')
    expect(ws.connect).toHaveBeenCalledWith(remote.url, 'new-token')
    wrapper.unmount()
  })

  it('keeps the stored credential when the token field is cleared on edit', async () => {
    const relays = useRelaysStore()
    const conn = useConnectionStore()
    const remote = relays.add('remote', 'wss://remote.example', 'saved-token')
    relays.setCurrent(remote.id)
    conn.status = 'connected'
    conn.url = remote.url
    const wrapper = mount(RelayManager)
    await wrapper.findAll('button[aria-label="Edit"]').at(-1)!.trigger('click')

    // clearing the pre-filled field means "keep the stored credential" — the
    // same meaning the add path gives an empty field via sessionTokenFor
    await wrapper.find('input[aria-label="Relay token"]').setValue('')
    await wrapper.find('.rm-form-actions .primary').trigger('click')

    const updated = relays.relays.find((r) => r.id === remote.id)
    expect(updated?.token).toBe('saved-token')
    expect(sessionTokenFor(remote.url)).toBe('saved-token')
    // url and token unchanged → no spurious reconnect of the current relay
    expect(ws.connect).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('adopts the new URL\'s own credential instead of migrating the old token', async () => {
    const relays = useRelaysStore()
    const a = relays.add('remote-a', 'wss://a.example', 'token-a')
    saveSessionToken('wss://b.example', 'token-b')
    const wrapper = mount(RelayManager)
    await wrapper.findAll('button[aria-label="Edit"]').at(-1)!.trigger('click')

    // the field starts prefilled with A's token; changing the URL clears it
    await wrapper.find('input[aria-label="Relay URL"]').setValue('wss://b.example')
    expect((wrapper.find('input[aria-label="Relay token"]').element as HTMLInputElement).value).toBe('')
    await wrapper.find('.rm-form-actions .primary').trigger('click')

    const updated = relays.relays.find((r) => r.id === a.id)
    expect(updated?.url).toBe('wss://b.example')
    // B's own credential is adopted — A's token must not land on B
    expect(updated?.token).toBe('token-b')
    expect(sessionTokenFor('wss://b.example')).toBe('token-b')
    expect(sessionTokenFor('wss://a.example')).toBe('')
    wrapper.unmount()
  })

  it('blocks saving a URL change to a token-requiring relay with no credential', async () => {
    const relays = useRelaysStore()
    const a = relays.add('remote-a', 'wss://a.example', 'token-a')
    const wrapper = mount(RelayManager)
    await wrapper.findAll('button[aria-label="Edit"]').at(-1)!.trigger('click')

    await wrapper.find('input[aria-label="Relay URL"]').setValue('wss://unseen.example')
    const saveBtn = wrapper.find('.rm-form-actions .primary')
    expect(saveBtn.attributes('disabled')).toBeDefined()
    await saveBtn.trigger('click')

    // save was gated: the entry is untouched and no credential moved
    const unchanged = relays.relays.find((r) => r.id === a.id)
    expect(unchanged?.url).toBe('wss://a.example')
    expect(unchanged?.token).toBe('token-a')
    expect(sessionTokenFor('wss://a.example')).toBe('token-a')
    expect(sessionTokenFor('wss://unseen.example')).toBe('')
    wrapper.unmount()
  })

  it('stores a retyped token for the new URL only', async () => {
    const relays = useRelaysStore()
    const a = relays.add('remote-a', 'wss://a.example', 'token-a')
    const wrapper = mount(RelayManager)
    await wrapper.findAll('button[aria-label="Edit"]').at(-1)!.trigger('click')

    await wrapper.find('input[aria-label="Relay URL"]').setValue('wss://b.example')
    await wrapper.find('input[aria-label="Relay token"]').setValue('token-b2')
    await wrapper.find('.rm-form-actions .primary').trigger('click')

    const updated = relays.relays.find((r) => r.id === a.id)
    expect(updated?.token).toBe('token-b2')
    expect(sessionTokenFor('wss://b.example')).toBe('token-b2')
    expect(sessionTokenFor('wss://a.example')).toBe('')
    wrapper.unmount()
  })
})
