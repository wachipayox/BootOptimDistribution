'use strict';

// Panel de perfiles → Ver revisiones → «Changelog»: edita el texto mutable de
// una revisión (PUT /v1/admin/revisions/{id}/changelog) y muestra el origen y
// los mods +/- que calcula el servidor. Usa request/make/setStatus/describeError
// de app.js; las funciones puras se exportan para las pruebas de Node.

function changelogChangesFromText(text) {
  return String(text || '').split(/\r?\n/).map(function (line) { return line.trim(); }).filter(Boolean);
}

function changelogChangesToText(changes) {
  return Array.isArray(changes) ? changes.join('\n') : '';
}

function changelogModLines(mods) {
  const lines = [];
  const m = mods || {};
  (m.added || []).forEach(function (mod) { lines.push('+ ' + mod.id + ' (' + mod.file + ')'); });
  (m.removed || []).forEach(function (mod) { lines.push('− ' + mod.id + ' (' + mod.file + ')'); });
  (m.updated || []).forEach(function (mod) { lines.push('~ ' + mod.id + ': ' + mod.from + ' → ' + mod.to); });
  return lines;
}

function changelogOriginText(entry, profileId) {
  const origin = entry && entry.origin;
  if (!origin || origin.profile_id === profileId) return 'Publicada en este perfil.';
  return 'Publicada desde ' + origin.profile_id + ' (versión ' + origin.sequence + ').';
}

function renderRevisionChangelog(profileId, revisionId) {
  const wrapper = make('div', 'card-actions');
  const button = make('button', 'button button-secondary', 'Changelog');
  button.type = 'button';
  const panel = make('div', 'changelog-editor');
  panel.hidden = true;
  wrapper.append(button, panel);

  button.addEventListener('click', async function () {
    if (!panel.hidden) {
      panel.hidden = true;
      return;
    }
    panel.hidden = false;
    panel.replaceChildren(make('p', 'card-meta', 'Cargando changelog…'));
    try {
      const payload = await request('/v1/profiles/' + encodeURIComponent(profileId) + '/changelog?revision=' + encodeURIComponent(revisionId));
      const entry = payload && payload.entries && payload.entries[0];
      if (!entry) throw new Error('El servidor no devolvió el changelog de esta revisión.');
      buildChangelogForm(panel, profileId, entry);
    } catch (error) {
      panel.replaceChildren(make('div', 'alert alert-error', describeError(error)));
    }
  });
  return wrapper;
}

function buildChangelogForm(panel, profileId, entry) {
  const originId = entry.origin.revision_id;
  const title = document.createElement('input');
  title.type = 'text';
  title.maxLength = 120;
  title.value = entry.title || '';
  const description = document.createElement('textarea');
  description.rows = 5;
  description.value = entry.description || '';
  const changes = document.createElement('textarea');
  changes.rows = 6;
  changes.value = changelogChangesToText(entry.changes);
  changes.placeholder = 'Un cambio por línea';

  const field = function (label, input) {
    const row = make('label', 'field');
    row.append(make('span', '', label), input);
    return row;
  };
  const status = make('p', 'inline-status');
  status.setAttribute('role', 'status');
  const save = make('button', 'button button-primary', 'Guardar changelog');
  save.type = 'button';
  save.addEventListener('click', async function () {
    save.disabled = true;
    setStatus(status, 'Guardando…');
    try {
      await request('/v1/admin/revisions/' + encodeURIComponent(originId) + '/changelog', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: title.value, description: description.value, changes: changelogChangesFromText(changes.value) })
      });
      setStatus(status, 'Changelog guardado.', 'success');
    } catch (error) {
      setStatus(status, describeError(error), 'error');
    } finally {
      save.disabled = false;
    }
  });

  const modLines = changelogModLines(entry.mods);
  const mods = make('pre', 'card-meta', modLines.length ? modLines.join('\n') : 'Sin cambios de mods.');
  const origin = make('p', 'card-meta', changelogOriginText(entry, profileId));
  const note = entry.origin.profile_id !== profileId
    ? make('p', 'card-meta', 'El texto se guarda en la revisión de origen y lo comparten los perfiles que la heredan.')
    : null;
  panel.replaceChildren(origin);
  if (note) panel.appendChild(note);
  panel.append(field('Título', title), field('Descripción', description), field('Cambios (uno por línea)', changes),
    make('strong', '', 'Mods'), mods, save, status);
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { changelogChangesFromText, changelogChangesToText, changelogModLines, changelogOriginText };
}
