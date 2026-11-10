import { afterEach, expect, it, vi } from 'vitest';
import { loadProjectSelection, rememberProjectSelection, PROJECT_SELECTION_KEY } from './selection.js';
const storage = () => { const values = new Map(); return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }; };
afterEach(() => vi.unstubAllGlobals());
it('remembers completed selection across refresh and uses it for a new tab', () => {
 const local = storage();vi.stubGlobal('localStorage', local);vi.stubGlobal('sessionStorage', storage());
 rememberProjectSelection('project-demo');expect(loadProjectSelection()).toBe('project-demo');
 vi.stubGlobal('sessionStorage', storage());expect(loadProjectSelection()).toBe('project-demo');
});
it('keeps this tab selection when another tab changes the browser preference', () => {
 const local = storage();vi.stubGlobal('localStorage', local);vi.stubGlobal('sessionStorage', storage());
 rememberProjectSelection('project-demo');local.setItem(PROJECT_SELECTION_KEY, 'project-qa');expect(loadProjectSelection()).toBe('project-demo');
});
it('ignores malformed identities and tolerates storage restrictions', () => {
 const local = storage(), tab = storage();vi.stubGlobal('localStorage', local);vi.stubGlobal('sessionStorage', tab);
 tab.setItem(PROJECT_SELECTION_KEY, 'bad\r\nproject');local.setItem(PROJECT_SELECTION_KEY, 'project-qa');expect(loadProjectSelection()).toBe('project-qa');
 const blocked = { getItem(){throw Error('blocked');},setItem(){throw Error('blocked');} };vi.stubGlobal('localStorage', blocked);vi.stubGlobal('sessionStorage', blocked);
 expect(loadProjectSelection()).toBe(null);expect(() => rememberProjectSelection('project-qa')).not.toThrow();
});
