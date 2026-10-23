'use strict';

// Versiones del launcher: publicar, editar notas/canal y retirar versiones de
// Pandora y del launcher Android. Usa request/qs/make/setStatus/formatBytes/
// hashFile/describeError de app.js.

const launcherPanel = { loading: false, releases: [], editing: null };
const LAUNCHER_APPS = { pandora: 'Pandora', android: 'Android' };
const LAUNCHER_CHANNELS = { player: 'Jugador', developer: 'Desarrollador' };
const LAUNCHER_PLATFORMS = ['windows-x86_64', 'linux-x86_64', 'macos-aarch64', 'android-universal'];

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
  const nameLabel = make('label');
  nameLabel.appendChild(make('span', '', 'Nombre de archivo'));
  const nameInput = make('input');
  nameInput.autocomplete = 'off';
  nameInput.maxLength = 128;
  nameInput.dataset.role = 'name';
  nameLabel.appendChild(nameInput);
  fileInput.addEventListener('change', function () {
    if (fileInput.files && fileInput.files[0] && !nameInput.value) nameInput.value = fileInput.files[0].name;
  });
  const remove = make('button', 'button button-secondary button-small', 'Quitar');
  remove.type = 'button';
  remove.addEventListener('click', function () { row.remove(); });
  row.append(platformLabel, fileLabel, nameLabel, remove);
  qs('#launcher-artifact-rows').appendChild(row);
}

function collectLauncherArtifacts() {
  return Array.from(qs('#launcher-artifact-rows').children).map(function (row) {
    const file = row.querySelector('[data-role="file"]').files[0];
    return {
      platform: row.querySelector('[data-role="platform"]').value.trim(),
      file_name: row.querySelector('[data-role="name"]').value.trim(),
      file: file
    };
  });
}

async function publishLauncherRelease(event) {
  event.preventDefault();
  const status = qs('#launcher-status');
  const button = qs('#launcher-publish');
  const artifacts = collectLauncherArtifacts();
  if (!artifacts.length || artifacts.some(function (a) { return !a.file || !a.platform || !a.file_name; })) {
    setStatus(status, 'Cada plataforma necesita nombre, binario y nombre de archivo.', 'error');
    return;
  }
  const app = qs('#launcher-app').value;
  const version = qs('#launcher-version').value.trim();
  button.disabled = true;
  try {
    const payload = [];
    for (let i = 0; i < artifacts.length; i += 1) {
      const item = artifacts[i];
      setStatus(status, 'Calculando SHA-256 de ' + item.file.name + '…');
      const digest = await hashFile(item.file);
      setStatus(status, 'Subiendo ' + item.file.name + ' (' + formatBytes(item.file.size) + ')…');
      await request('/v1/admin/objects/sha256/' + encodeURIComponent(digest), {
        method: 'POST',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: item.file
      });
      payload.push({ platform: item.platform, file_name: item.file_name, object: { sha256: digest, size: item.file.size } });
    }
    setStatus(status, 'Firmando y publicando…');
    await request('/v1/admin/launcher-releases', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        app: app, version: version, channel: qs('#launcher-channel').value,
        notes: qs('#launcher-notes').value, artifacts: payload
      })
    });
    setStatus(status, 'Versión ' + version + ' de ' + LAUNCHER_APPS[app] + ' publicada. Ese número ya no se podrá reutilizar.', 'success');
    qs('#launcher-version').value = '';
    qs('#launcher-notes').value = '';
    qs('#launcher-artifact-rows').replaceChildren();
    addLauncherArtifactRow('');
    await loadLauncherReleases();
  } catch (error) {
    setStatus(status, describeError(error), 'error');
  } finally {
    button.disabled = false;
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
