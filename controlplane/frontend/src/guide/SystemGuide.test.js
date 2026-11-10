// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import SystemGuide from './SystemGuide.vue';

let wrapper;
afterEach(() => { wrapper?.unmount(); wrapper = null; vi.unstubAllGlobals(); });
const render = () => { wrapper = mount(SystemGuide); return wrapper; };
const click = async name => { await wrapper.findAll('button').find(button => button.text() === name).trigger('click'); await flushPromises(); };

it('introduces the full workflow and emits only a destination without fetching or creating data', async () => {
 const fetch = vi.fn(); vi.stubGlobal('fetch', fetch); render();
 expect(wrapper.get('h1').text()).toBe('使用指引');
 expect(wrapper.get('[aria-label="指引主题"]').findAll('button')).toHaveLength(12);
 expect(wrapper.text()).toContain('先调试，再运行');
 await wrapper.get('[data-guide-article="start-environment"] button').trigger('click');
 expect(wrapper.emitted('navigate')).toEqual([['environments']]); expect(fetch).not.toHaveBeenCalled();
});
it('searches across topics case-insensitively and describes reserved gRPC execution accurately', async () => {
 render(); await wrapper.get('input[aria-label="搜索使用指引"]').setValue('  GrPc  ');
 expect(wrapper.text()).toContain('RPC、gRPC 与 Protobuf');
 expect(wrapper.text()).toContain('尚未提供在线执行适配器');
 expect(wrapper.findAll('[data-guide-article]')).toHaveLength(1);
 await click('清空搜索'); expect(wrapper.get('[data-guide-article="start-environment"]').exists()).toBe(true);
});
it('reveals an answer matched only in FAQ body and handles unmatched special characters as text', async () => {
 render(); await wrapper.get('input').setValue('错误码');
 const answer=wrapper.get('[data-guide-article="faq-status"]'); expect(answer.element.tagName).toBe('DETAILS'); expect(answer.attributes('open')).toBeDefined();
 expect(answer.text()).toContain('不会默认');
 await wrapper.get('input').setValue('<img src=x onerror=alert(1)>');
 expect(wrapper.text()).toContain('没有找到相关指引'); expect(wrapper.find('img').exists()).toBe(false);
 await click('清空搜索'); expect(wrapper.find('[data-guide-article="start-environment"]').exists()).toBe(true);
});
it('switches topics and resets search without writing preferences or executing examples', async () => {
 const setItem=vi.fn(); vi.stubGlobal('localStorage',{setItem}); render();
 await wrapper.get('input').setValue('JVM'); await click('项目与环境');
 expect(wrapper.get('input').element.value).toBe(''); expect(wrapper.text()).toContain('{{$random.hex(32)}}');
 expect(wrapper.get('[aria-current="true"]').text()).toBe('项目与环境');
 expect(setItem).not.toHaveBeenCalled(); expect(wrapper.emitted('navigate')).toBeUndefined();
});
