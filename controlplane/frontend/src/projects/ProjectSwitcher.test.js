// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/vue';
import ProjectSwitcher from './ProjectSwitcher.vue';
const projects = [{ id: 'one', name: '当前项目' }, { id: 'two', name: '很长的项目名称也应该完整显示，不会把菜单撑出窗口', archived: true }];
afterEach(cleanup);
it('opens a themed listbox, keeps the selected project and emits exact option identity', async () => {
  const { emitted } = render(ProjectSwitcher, { props: { projects, modelValue: 'one' } });
  const trigger = screen.getByRole('combobox', { name: '当前项目' });
  expect(trigger.getAttribute('aria-expanded')).toBe('false');
  await fireEvent.click(trigger);
  expect(screen.getByRole('listbox', { name: '选择项目' })).toBeTruthy();
  expect(screen.getByRole('option', { name: '当前项目' }).getAttribute('aria-selected')).toBe('true');
  await fireEvent.click(screen.getByRole('option', { name: /很长的项目名称/ }));
  expect(emitted().select).toEqual([['two']]);
  expect(screen.queryByRole('listbox')).toBeNull();
});
it('supports arrow keyboard selection and Escape without switching', async () => {
  const { emitted } = render(ProjectSwitcher, { props: { projects, modelValue: 'one' } });
  const trigger = screen.getByRole('combobox', { name: '当前项目' }); trigger.focus();
  await fireEvent.keyDown(trigger, { key: 'ArrowDown' });
  await fireEvent.keyDown(trigger, { key: 'ArrowDown' });
  await fireEvent.keyDown(trigger, { key: 'Enter' });
  expect(emitted().select).toEqual([['two']]);
  await fireEvent.click(trigger); await fireEvent.keyDown(trigger, { key: 'Escape' });
  expect(screen.queryByRole('listbox')).toBeNull();
  expect(document.activeElement).toBe(trigger);
});
it('closes when clicking outside and never opens while disabled', async () => {
  const { rerender } = render(ProjectSwitcher, { props: { projects, modelValue: 'one' } });
  const trigger = screen.getByRole('combobox', { name: '当前项目' });
  await fireEvent.click(trigger); await fireEvent.pointerDown(document.body);
  expect(screen.queryByRole('listbox')).toBeNull();
  await rerender({ disabled: true }); await fireEvent.click(trigger);
  expect(screen.queryByRole('listbox')).toBeNull();
});
