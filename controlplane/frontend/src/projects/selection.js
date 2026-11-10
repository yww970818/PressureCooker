export const PROJECT_SELECTION_KEY = 'pressurecooker.selected-project';
const storageNames = ['sessionStorage', 'localStorage'];
const validProject = value => typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$/.test(value);

// Each tab keeps its own selection; a new tab inherits the browser preference.
export function loadProjectSelection() {
  for (const name of storageNames) {
    try {
      const value = globalThis[name]?.getItem(PROJECT_SELECTION_KEY);
      if (validProject(value)) return value;
    } catch { /* Storage restrictions must not prevent opening the workspace. */ }
  }
  return null;
}

export function rememberProjectSelection(id) {
  if (!validProject(id)) return;
  for (const name of storageNames) {
    try { globalThis[name]?.setItem(PROJECT_SELECTION_KEY, id); }
    catch { /* The current in-memory selection still works without storage. */ }
  }
}
