'use strict';

// Renaming files in the publication draft. A rename is a removal of the old path
// plus an addition at the new one, so the manifest only ever sees ordinary
// changes: the old file is dropped (or tombstoned if inherited) and the new path
// is a new entry. "Replace file" with a differently named file reuses the same
// move, so the replacement takes the new file's name.

function pathBasename(path) {
  return path.split('/').pop();
}

function pathWithName(path, name) {
  const slash = path.lastIndexOf('/');
  return slash < 0 ? name : path.slice(0, slash + 1) + name;
}

function validFileName(name) {
  return Boolean(name) && !name.includes('/') && !name.includes('\\') && name !== '.' && name !== '..';
}

// The bytes of a draft file as a File named for its destination: the local copy
// if there is one, otherwise the published object fetched from the service.
async function draftFileContent(path, newName) {
  const local = state.selectedFiles.get(path);
  if (local && local.file) return new File([local.file], newName, { type: local.mediaType || '' });
  const published = state.parentMap.get(path);
  const digest = objectDigest(published);
  if (!digest) throw new Error('No hay una huella publicada para este archivo.');
  const response = await fetch('/v1/objects/sha256/' + encodeURIComponent(digest), { credentials: 'same-origin' });
  if (!response.ok) throw new Error('HTTP ' + response.status);
  return new File([await response.blob()], newName, { type: (published.object && published.object.media_type) || '' });
}

// Moves oldPath to newPath. `file` is the new content (replace), or omitted to keep the content (rename).
async function moveDraftFile(oldPath, newPath, file, label) {
  const draft = draftWorkbenchMap();
  const fail = function (message) { setStatus(nodes.folderStatus, message, 'error'); };
  if (!draft.has(oldPath)) return fail('El archivo ya no forma parte de esta rama.');
  if (!validatePath(newPath)) return fail('El nombre da una ruta no válida.');
  if (draft.has(newPath)) return fail('Ya existe un archivo llamado ' + pathBasename(newPath) + ' en esa carpeta.');
  if (classifyPath(newPath) !== classifyPath(oldPath)) {
    return fail('El nuevo nombre cambiaría el tipo de archivo (' + kindLabel(classifyPath(oldPath)) + '). Un mod debe seguir terminando en .jar.');
  }
  const published = state.parentMap.get(oldPath);
  if (state.parentRef && published && published.kind !== 'mod' && published.kind !== 'config' && !updateOwnObject(oldPath)) {
    return fail('No se puede retirar este tipo de archivo heredado, así que tampoco renombrarlo.');
  }
  let content = file;
  if (!content) {
    try { content = await draftFileContent(oldPath, pathBasename(newPath)); } catch (error) {
      return fail('No se pudo leer el archivo para renombrarlo: ' + describeError(error));
    }
  }

  state.selectedFiles.delete(oldPath);
  if (state.parentRef && published) state.removedPaths.add(oldPath);
  else state.removedPaths.delete(oldPath);
  state.modIds.delete(oldPath);
  if (state.policies.has(oldPath)) {
    state.policies.set(newPath, state.policies.get(oldPath));
    state.policies.delete(oldPath);
  }
  // This branch's own per-setting rules follow the file; inherited rules stay with the parent's path.
  Array.from(state.localConfigSettings.values()).filter(function (rule) { return rule.path === oldPath; }).forEach(function (rule) {
    state.localConfigSettings.delete(configRuleID(oldPath, rule.key));
    state.localConfigSettings.set(configRuleID(newPath, rule.key), { ...rule, path: newPath });
  });
  state.pendingReplacePath = '';
  await scanRecords([{ path: newPath, file: content }], label || 'Archivo renombrado', 'overlay');
}

// "Replace file" picked a file with another name: it takes that name.
function replaceDraftFileWithNewName(oldPath, file) {
  const newPath = pathWithName(oldPath, file.name);
  state.pendingReplacePath = '';
  state.pendingFolderPath = '';
  return moveDraftFile(oldPath, newPath, file, 'Archivo reemplazado y renombrado a ' + file.name);
}

function openRenameFile(path) {
  state.pendingRenamePath = path;
  qs('#rename-file-current').textContent = path;
  const input = qs('#rename-file-name');
  input.value = pathBasename(path);
  qs('#rename-file-dialog').showModal();
  input.focus();
  const dot = input.value.lastIndexOf('.');
  input.setSelectionRange(0, dot > 0 ? dot : input.value.length);
}

function renameFileFromDialog() {
  const path = state.pendingRenamePath;
  const name = qs('#rename-file-name').value.trim();
  if (!validFileName(name)) {
    setStatus(nodes.folderStatus, 'Escribe un nombre de archivo válido, sin separadores.', 'error');
    return;
  }
  qs('#rename-file-dialog').close('save');
  state.pendingRenamePath = '';
  if (name === pathBasename(path)) return;
  moveDraftFile(path, pathWithName(path, name));
}

document.querySelector('#rename-file-form').addEventListener('submit', function (event) {
  if (event.submitter && event.submitter.id === 'rename-file-save') {
    event.preventDefault();
    renameFileFromDialog();
  }
});
