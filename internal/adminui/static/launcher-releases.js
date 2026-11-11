'use strict';

// Versiones del launcher: publicar, editar notas/canal y retirar versiones de
// Pandora y del launcher Android. Usa request/qs/make/setStatus/formatBytes/
// hashFile/describeError de app.js.

const launcherPanel = { loading: false, releases: [], editing: null };
const LAUNCHER_APPS = { pandora: 'Pandora', android: 'Android' };
const LAUNCHER_CHANNELS = { player: 'Jugador', developer: 'Desarrollador' };
const LAUNCHER_PLATFORMS = ['windows-x86_64', 'windows-aarch64', 'android-universal'];

function launcherDate(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString();
}

function launcherReleaseRow(release) {
  const row = make('tr');
  if (release.withdrawn_at) row.className = 'is-withdrawn';
  const platforms = (release.artifacts || []).map(function (a) { return a.platform + ' (' + formatBytes(a.size) + ')'; }).join(', ');
  [release.version, LAUNCHER_CHANNELS[release.channel] || release.channel, launcherDate(release.published_at), platforms || '—',
    release.withdrawn_at ? 'Retirada el ' + launcherDate(release.withdrawn_at) : 'Ofrecida']
    .forEach(function (text) { row.appendChild(make('td', '', text)); });
  const actions = make('td');
  const edit = make('button', 'button button-secondary button-small', 'Editar');
  edit.type = 'button';
  edit.addEventListener('click', function () { startLauncherEdit(release); });
  actions.appendChild(edit);
  if (!release.withdrawn_at) {
    const withdraw = make('button', 'button button-danger button-small', 'Retirar');
    withdraw.type = 'button';
    withdraw.addEventListener('click', function () { withdrawLauncherRelease(release); });
    actions.append(' ', withdraw);
  }
  row.appendChild(actions);
  return row;
}

function renderLauncherReleases() {
  const host = qs('#launcher-release-lists');
  const nodes = Object.keys(LAUNCHER_APPS).map(function (app) {
    const section = make('div', 'launcher-app-list');
    section.appendChild(make('h4', 'test-api-subtitle', LAUNCHER_APPS[app]));
    const releases = launcherPanel.releases.filter(function (r) { return r.app === app; });
    if (!releases.length) {
      section.appendChild(make('p', 'microcopy', 'Todavía no hay versiones publicadas.'));
      return section;
    }
    const table = make('table', 'test-profile-table');
    const head = make('tr');
    ['Versión', 'Canal', 'Publicada', 'Plataformas', 'Estado'].forEach(function (name) {
      const th = make('th', '', name);
      th.scope = 'col';
      head.appendChild(th);
    });
    const actionsHead = make('th');
    actionsHead.scope = 'col';
    actionsHead.appendChild(make('span', 'visually-hidden', 'Acciones'));
    head.appendChild(actionsHead);
    const thead = make('thead');
    thead.appendChild(head);
    const tbody = make('tbody');
    releases.forEach(function (release) { tbody.appendChild(launcherReleaseRow(release)); });
    table.append(thead, tbody);
    const wrap = make('div', 'test-profile-table-wrap');
    wrap.appendChild(table);
    section.appendChild(wrap);
    return section;
  });
  host.replaceChildren.apply(host, nodes);
}

async function loadLauncherReleases() {
  if (launcherPanel.loading) return;
  launcherPanel.loading = true;
  const status = qs('#launcher-status');
  try {
    const result = await request('/v1/admin/launcher-releases');
    launcherPanel.releases = result.releases || [];
    renderLauncherReleases();
  } catch (error) {
    setStatus(status, describeError(error), 'error');
  } finally {
    launcherPanel.loading = false;
  }
}

// The version and the download name come from the binary itself: choosing a
// file uploads it and asks the server to read them (POST …/inspect). They are
// shown read-only; the server checks them again when publishing.
function addLauncherArtifactRow(platform) {
  const row = make('div', 'launcher-artifact-row');
  const platformLabel = make('label');
  platformLabel.appendChild(make('span', '', 'Plataforma'));
  const platformInput = make('input');
  platformInput.setAttribute('list', 'launcher-platform-options');
  platformInput.autocomplete = 'off';
  platformInput.value = platform || '';
  platformInput.dataset.role = 'platform';
  platformLabel.appendChild(platformInput);
  const fileLabel = make('label');
  fileLabel.appendChild(make('span', '', 'Binario'));
  const fileInput = make('input');
  fileInput.type = 'file';
  fileInput.dataset.role = 'file';
  fileLabel.appendChild(fileInput);
  const read = make('p', 'launcher-artifact-read microcopy', 'Elige la plataforma y el binario: la versión y el nombre se leen del propio archivo.');
  read.dataset.role = 'read';
  const remove = make('button', 'button button-secondary button-small', 'Quitar');
  remove.type = 'button';
  remove.addEventListener('click', function () { row.remove(); refreshLauncherSummary(); });
  const inspect = function () { inspectLauncherRow(row); };
  fileInput.addEventListener('change', inspect);
  platformInput.addEventListener('change', inspect);
  qs('#launcher-app').addEventListener('change', inspect);
  row.append(platformLabel, fileLabel, remove, read);
  qs('#launcher-artifact-rows').appendChild(row);
}

async function inspectLauncherRow(row) {
  if (!row.isConnected) return;
  const read = row.querySelector('[data-role="read"]');
  const file = row.querySelector('[data-role="file"]').files[0];
  const platform = row.querySelector('[data-role="platform"]').value.trim();
  const token = {};
  row.launcherToken = token;
  row.launcherArtifact = null;
  refreshLauncherSummary();
  if (!file || !platform) return;
  read.className = 'launcher-artifact-read microcopy';
  try {
    read.textContent = 'Calculando SHA-256 de ' + file.name + '…';
    const digest = await hashFile(file);
    if (row.launcherToken !== token) return;
    read.textContent = 'Subiendo ' + file.name + ' (' + formatBytes(file.size) + ')…';
    await request('/v1/admin/objects/sha256/' + encodeURIComponent(digest), {
      method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: file
    });
    read.textContent = 'Leyendo la versión del binario…';
    const object = { sha256: digest, size: file.size };
    const result = await request('/v1/admin/launcher-releases/inspect', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ app: qs('#launcher-app').value, platform: platform, object: object })
    });
    if (row.launcherToken !== token) return;
    row.launcherArtifact = { platform: platform, object: object, version: result.version, file_name: result.file_name };
    read.replaceChildren(
      make('strong', '', 'Versión ' + result.version), document.createTextNode(' · '),
      make('code', '', result.file_name), document.createTextNode(' · ' + result.detail)
    );
  } catch (error) {
    if (row.launcherToken !== token) return;
    read.className = 'launcher-artifact-read inline-status error';
    read.textContent = describeError(error);
  }
  refreshLauncherSummary();
}

// Read-only summary of what will be published; publishing needs every row
// read and all of them carrying the same version.
function launcherDraft() {
  const rows = Array.from(qs('#launcher-artifact-rows').children);
  const artifacts = rows.map(function (row) { return row.launcherArtifact; });
  if (!rows.length || artifacts.some(function (a) { return !a; })) return { error: 'Cada plataforma necesita un binario leído sin errores.' };
  const versions = Array.from(new Set(artifacts.map(function (a) { return a.version; })));
  if (versions.length !== 1) return { error: 'Los binarios llevan versiones distintas (' + versions.join(', ') + '); una versión publica binarios de la misma versión.' };
  return { version: versions[0], artifacts: artifacts };
}

function refreshLauncherSummary() {
  const draft = launcherDraft();
  qs('#launcher-version').value = draft.version || '';
  qs('#launcher-publish').disabled = Boolean(draft.error);
}

async function publishLauncherRelease(event) {
  event.preventDefault();
  const status = qs('#launcher-status');
  const button = qs('#launcher-publish');
  const draft = launcherDraft();
  if (draft.error) {
    setStatus(status, draft.error, 'error');
    return;
  }
  const app = qs('#launcher-app').value;
  if (!window.confirm('¿Publicar ' + LAUNCHER_APPS[app] + ' ' + draft.version + '? Ese número no se podrá volver a usar, ni retirándola.')) return;
  button.disabled = true;
  try {
    setStatus(status, 'Firmando y publicando…');
    await request('/v1/admin/launcher-releases', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        app: app, version: draft.version, channel: qs('#launcher-channel').value,
        notes: qs('#launcher-notes').value,
        artifacts: draft.artifacts.map(function (a) { return { platform: a.platform, file_name: a.file_name, object: a.object }; })
      })
    });
    setStatus(status, 'Versión ' + draft.version + ' de ' + LAUNCHER_APPS[app] + ' publicada. Ese número ya no se podrá reutilizar.', 'success');
    qs('#launcher-notes').value = '';
    qs('#launcher-artifact-rows').replaceChildren();
    addLauncherArtifactRow('');
    refreshLauncherSummary();
    await loadLauncherReleases();
  } catch (error) {
    setStatus(status, describeError(error), 'error');
    refreshLauncherSummary();
  }
}

function startLauncherEdit(release) {
  launcherPanel.editing = release;
  qs('#launcher-edit-title').textContent = 'Editar ' + LAUNCHER_APPS[release.app] + ' ' + release.version;
  qs('#launcher-edit-channel').value = release.channel;
  qs('#launcher-edit-notes').value = release.notes || '';
  qs('#launcher-edit').hidden = false;
  qs('#launcher-edit-notes').focus();
}

async function saveLauncherEdit(event) {
  event.preventDefault();
  const release = launcherPanel.editing;
  if (!release) return;
  const status = qs('#launcher-status');
  try {
    await request('/v1/admin/launcher-releases/' + encodeURIComponent(release.app) + '/' + encodeURIComponent(release.version), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ channel: qs('#launcher-edit-channel').value, notes: qs('#launcher-edit-notes').value })
    });
    qs('#launcher-edit').hidden = true;
    launcherPanel.editing = null;
    setStatus(status, 'Cambios guardados. El binario y su firma no cambian.', 'success');
    await loadLauncherReleases();
  } catch (error) {
    setStatus(status, describeError(error), 'error');
  }
}

async function withdrawLauncherRelease(release) {
  const label = LAUNCHER_APPS[release.app] + ' ' + release.version;
  if (!window.confirm('¿Retirar ' + label + '? Dejará de ofrecerse y su binario dejará de descargarse. El número no se podrá volver a publicar y quien ya la tenga no bajará de versión.')) return;
  const status = qs('#launcher-status');
  try {
    await request('/v1/admin/launcher-releases/' + encodeURIComponent(release.app) + '/' + encodeURIComponent(release.version), { method: 'DELETE' });
    setStatus(status, label + ' retirada.', 'success');
    await loadLauncherReleases();
  } catch (error) {
    setStatus(status, describeError(error), 'error');
  }
}

(function setupLauncherPanel() {
  const datalist = qs('#launcher-platform-options');
  LAUNCHER_PLATFORMS.forEach(function (platform) {
    const option = make('option');
    option.value = platform;
    datalist.appendChild(option);
  });
  addLauncherArtifactRow('');
  refreshLauncherSummary();
  qs('#launcher-add-artifact').addEventListener('click', function () { addLauncherArtifactRow(''); });
  qs('#launcher-form').addEventListener('submit', publishLauncherRelease);
  qs('#launcher-edit').addEventListener('submit', saveLauncherEdit);
  qs('#launcher-edit-cancel').addEventListener('click', function () { qs('#launcher-edit').hidden = true; launcherPanel.editing = null; });
  qs('#launcher-refresh').addEventListener('click', loadLauncherReleases);
  window.addEventListener('hashchange', function () {
    if (window.location.hash === '#launcher') loadLauncherReleases();
  });
  window.addEventListener('load', function () { if (window.location.hash === '#launcher') loadLauncherReleases(); });
})();
