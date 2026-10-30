import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SessionMenu from './SessionMenu.vue'
import { ws } from '../../api/ws'

vi.mock('../../api/ws', () => ({
  ws: { send: vi.fn(() => true) },
}))

function mountMenu(label = 'herdrem') {
  return mount(SessionMenu, {
    props: {
      spaceId: 'ws1',
      label,
      host: 'local',
      linked: false,
      pane: null,
      mode: 'workspace',
      index: 0,
      count: 1,
      canManage: true,
      canReorder: false,
      canWorktree: true,
    },
  })
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear()
})

describe('SessionMenu worktree create', () => {
  it('asks for a name defaulting to the space plus a random suffix', async () => {
    const wrapper = mountMenu()
    await wrapper.get('[data-action=create-worktree]').trigger('click')
    expect((wrapper.get('input[name=label]').element as HTMLInputElement).value).toMatch(/^herdrem-[0-9a-f]{4}$/)
  })

  it('sends the edited name as the new workspace label', async () => {
    const wrapper = mountMenu()
    await wrapper.get('[data-action=create-worktree]').trigger('click')
    await wrapper.get('input[name=label]').setValue('herdrem-notes')
    await wrapper.get('form').trigger('submit')
    expect(ws.send).toHaveBeenCalledWith(expect.objectContaining({
      type: 'worktree_create',
      workspace_id: 'ws1',
      host: 'local',
      label: 'herdrem-notes',
    }))
  })
})
