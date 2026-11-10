import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { apiRequestKey, loadApiSelection, loadEnvironmentSelection, loadWorkspaceView, rememberApiSelection, rememberEnvironmentSelection, rememberWorkspaceView } from './view-state.js';

const storage = () => {
  const values = new Map();
  return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)), entries: () => [...values] };
};
beforeEach(() => { vi.stubGlobal('sessionStorage', storage()); vi.stubGlobal('localStorage', storage()); });
afterEach(() => vi.unstubAllGlobals());

it('restores each project view and report independently, preferring this tab', () => {
  rememberWorkspaceView('project-one', { page: 'reports', reportRunId: 'run-41' });
  rememberWorkspaceView('project-two', { page: 'apis', reportRunId: '' });
  localStorage.setItem('pressurecooker.workspace-view.project-one', JSON.stringify({ page: 'plans', reportRunId: '' }));
  expect(loadWorkspaceView('project-one')).toEqual({ page: 'reports', reportRunId: 'run-41' });
  expect(loadWorkspaceView('project-two')).toEqual({ page: 'apis', reportRunId: '' });
  expect(loadWorkspaceView('project-new')).toEqual({ page: 'runs', reportRunId: '' });
});
it('remembers the system guide as a valid page for each project', () => {
 rememberWorkspaceView('project-one', { page: 'guide', reportRunId: '' });
 expect(loadWorkspaceView('project-one')).toEqual({ page: 'guide', reportRunId: '' });
 expect(loadWorkspaceView('project-two').page).toBe('runs');
});
it('uses browser state when tab state is missing or malformed', () => {
  localStorage.setItem('pressurecooker.workspace-view.project-one', JSON.stringify({ page: 'datasets', reportRunId: 'run-2' }));
  sessionStorage.setItem('pressurecooker.workspace-view.project-one', '{bad json');
  expect(loadWorkspaceView('project-one')).toEqual({ page: 'datasets', reportRunId: 'run-2' });
  sessionStorage.setItem('pressurecooker.workspace-view.project-one', JSON.stringify({ page: 'https://private/?token=secret' }));
  expect(loadWorkspaceView('project-one').page).toBe('datasets');
});
it('keeps an explicit no-environment choice instead of restoring the browser preference', () => {
  rememberEnvironmentSelection('project-one', 'environment-1');
  rememberEnvironmentSelection('project-two', 'environment-2');
  sessionStorage.setItem('pressurecooker.environment-selection.project-one', '');
  expect(loadEnvironmentSelection('project-one')).toBe('');
  expect(loadEnvironmentSelection('project-two')).toBe('environment-2');
  rememberEnvironmentSelection('project-two', '');
  expect(localStorage.getItem('pressurecooker.environment-selection.project-two')).toBe('');
});
it('falls back past an invalid environment and rejects unsafe project/resource identifiers', () => {
  localStorage.setItem('pressurecooker.environment-selection.project-one', 'environment-1');
  sessionStorage.setItem('pressurecooker.environment-selection.project-one', 'https://secret/');
  expect(loadEnvironmentSelection('project-one')).toBe('environment-1');
  rememberEnvironmentSelection('project-one', 'secret?token=credential');
  expect(loadEnvironmentSelection('project-one')).toBe('environment-1');
  rememberWorkspaceView('../project-secret', { page: 'apis', reportRunId: 'run-1' });
  expect(loadWorkspaceView('../project-secret')).toEqual({ page: 'runs', reportRunId: '' });
  expect(sessionStorage.entries().some(([key]) => key.includes('../'))).toBe(false);
});
it('restores API IDs and a request fingerprint without saving payload fields', () => {
  const requestKey = apiRequestKey('get', '/private?token=secret');
  rememberApiSelection('project-one', { collectionId: 'collection-1', requestKey, view: 'environment', url: 'https://secret/', body: 'private', headers: { Authorization: 'secret' } });
  expect(loadApiSelection('project-one')).toEqual({ collectionId: 'collection-1', requestKey, view: 'request' });
  expect(loadApiSelection('project-two')).toEqual({ collectionId: '', requestKey: '', view: 'request' });
  const stored = JSON.stringify([...sessionStorage.entries(), ...localStorage.entries()]);
  expect(stored).not.toMatch(/private|secret|Authorization|headers|url|body/);
});
it('validates restored and remembered state without persisting unsafe identifiers', () => {
  rememberWorkspaceView('project-one', { page: 'reports', reportRunId: 'run?token=secret', token: 'private' });
  rememberApiSelection('project-one', { collectionId: 'x'.repeat(102), requestKey: '/private?secret', view: 'invalid', body: 'secret' });
  expect(loadWorkspaceView('project-one')).toEqual({ page: 'reports', reportRunId: '' });
  expect(loadApiSelection('project-one')).toEqual({ collectionId: '', requestKey: '', view: 'request' });
  expect(JSON.stringify([...sessionStorage.entries(), ...localStorage.entries()])).not.toMatch(/secret|private|token/);
});
it('falls back safely when storage getters, reads, or writes fail', () => {
  rememberApiSelection('project-one', { collectionId: 'collection-1', requestKey: '', view: 'project-environment' });
  vi.stubGlobal('sessionStorage', { getItem() { throw new Error('restricted'); }, setItem() { throw new Error('restricted'); } });
  expect(loadApiSelection('project-one').view).toBe('request');
  rememberEnvironmentSelection('project-one', 'environment-2');
  expect(localStorage.getItem('pressurecooker.environment-selection.project-one')).toBe('environment-2');
  vi.stubGlobal('localStorage', undefined);
  expect(() => rememberWorkspaceView('project-one', { page: 'plans', reportRunId: '' })).not.toThrow();
  expect(loadWorkspaceView('project-one')).toEqual({ page: 'runs', reportRunId: '' });
});
it('fingerprints method and path stably while distinguishing different requests', () => {
  expect(apiRequestKey('get', '/orders?token=secret')).toMatch(/^[a-f0-9]{16}$/);
  expect(apiRequestKey('get', '/orders')).toBe(apiRequestKey('GET', '/orders'));
  expect(apiRequestKey('GET', '/orders')).not.toBe(apiRequestKey('POST', '/orders'));
  expect(apiRequestKey('GET', '/orders')).not.toBe(apiRequestKey('GET', '/orders/1'));
});
it('falls back when reading the storage property itself is restricted', () => {
  const browser = localStorage;
  browser.setItem('pressurecooker.environment-selection.project-one', 'environment-1');
  Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, get() { throw new Error('restricted getter'); } });
  expect(loadEnvironmentSelection('project-one')).toBe('environment-1');
  rememberEnvironmentSelection('project-one', 'environment-2');
  expect(browser.getItem('pressurecooker.environment-selection.project-one')).toBe('environment-2');
});
