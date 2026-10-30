import { beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import LineCommentBar from './LineCommentBar.vue';
import { formatLineRef, buildCommentText } from '../../utils/lineRef';
import { useAgentsStore } from '../../stores/connection';
import { ws } from '../../api/ws';

vi.mock('../../api/ws', () => ({
  ws: { send: vi.fn(() => true) },
}));

function seedPane(status: string) {
  const agents = useAgentsStore();
  agents.setAgents([{ pane_id: 'w1:p1', kind: 'agent', agent: 'claude', status, host: 'local', workspace_id: 'ws1' }]);
}

function mountBar(props: Record<string, unknown> = {}) {
  return mount(LineCommentBar, {
    props: { path: 'src/app.ts', start: 10, end: 14, paneId: 'w1:p1', ...props },
  });
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.mocked(ws.send).mockClear();
});

describe('formatLineRef', () => {
  it('renders a single line and a normalized range', () => {
    expect(formatLineRef('a.ts', 3, 3)).toBe('a.ts:3');
    expect(formatLineRef('a.ts', 4, 8)).toBe('a.ts:4-8');
    expect(formatLineRef('a.ts', 8, 4)).toBe('a.ts:4-8');
  });
});

describe('LineCommentBar', () => {
  it('sends path:range plus the comment via send_text with submit', async () => {
    seedPane('working');
    const wrapper = mountBar();
    await wrapper.get('.lcb-input').setValue('rename this helper');
    await wrapper.get('.lcb-send').trigger('click');
    expect(ws.send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'send_text',
        pane_id: 'w1:p1',
        text: 'src/app.ts:10-14\nrename this helper',
        submit: true,
      }),
    );
    expect(wrapper.emitted('sent')).toHaveLength(1);
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  it('single-line selection sends path:line', () => {
    expect(buildCommentText('a.ts', 7, 7, 'why?')).toBe('a.ts:7\nwhy?');
  });

  it('disables the send when the pane is blocked', async () => {
    seedPane('blocked');
    const wrapper = mountBar();
    await wrapper.get('.lcb-input').setValue('looks wrong');
    const send = wrapper.get('.lcb-send');
    expect(send.attributes('disabled')).toBeDefined();
    expect(wrapper.text()).toContain('waiting for approval');
    await send.trigger('click');
    expect(ws.send).not.toHaveBeenCalled();
  });

  it('disables when the pane is unknown', () => {
    const wrapper = mountBar({ paneId: 'w9:pX' });
    expect(wrapper.get('.lcb-send').attributes('disabled')).toBeDefined();
    expect(wrapper.text()).toContain('Pane unavailable');
  });

  it('close clears via the × button and Escape', async () => {
    seedPane('idle');
    const wrapper = mountBar();
    await wrapper.get('.lcb-close').trigger('click');
    expect(wrapper.emitted('close')).toHaveLength(1);
    await wrapper.get('.lcb-input').trigger('keydown', { key: 'Escape' });
    expect(wrapper.emitted('close')!.length).toBeGreaterThanOrEqual(2);
  });
});
