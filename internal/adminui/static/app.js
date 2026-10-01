'use strict';

const MAX_INHERITANCE_LEVELS = 8;

const state = {
  icon: null,
  iconURL: null,
  overview: null,
  build: {},
  profiles: [],
  recentRevisions: [],
  parentMap: new Map(),
  parentRef: null,
  parentDepth: 0,
  parentManifest: null,
  folderSelected: false,
  snapshotMode: false,
  selectedFiles: new Map(),
  removedPaths: new Set(),
  createdDirs: new Set(),
  collapsedDirs: new Set(),
  pendingReplacePath: '',
  pendingFolderPath: '',
  pendingNewFolderParent: '',
  editorPath: '',
  pathProblems: [],
  diff: [],
  policies: new Map(),
  parentConfigSettings: new Map(),
  localConfigSettings: new Map(),
  modIds: new Map(),
  manifest: null,
  canonicalManifest: '',
  manifestSHA256: '',
  staged: false,
  csrfToken: '',
  serviceUpdate: null,
  overviewSource: '',
  apiErrors: [],
  minecraftVersions: [],
  neoForgeVersions: []
};

const nodes = {
  apiState: document.querySelector('#api-state'),
  alerts: document.querySelector('#global-alerts'),
  overviewProfiles: document.querySelector('#overview-profiles'),
  profilesList: document.querySelector('#profiles-list'),
  activity: document.querySelector('#activity-list'),
  parentProfile: document.querySelector('#parent-profile'),
  parentRevision: document.querySelector('#parent-revision'),
  parentStatus: document.querySelector('#parent-status'),
  pickDirectory: document.querySelector('#pick-directory'),
  addFiles: document.querySelector('#add-files'),
  createFolder: document.querySelector('#create-folder'),
  filePicker: document.querySelector('#file-picker'),
  folderFallback: document.querySelector('#folder-fallback'),
  loadSynthetic: document.querySelector('#load-synthetic'),
  folderStatus: document.querySelector('#folder-status'),
  pickerSupport: document.querySelector('#picker-support'),
  syntheticHint: document.querySelector('#synthetic-hint'),
  diffBody: document.querySelector('#diff-body'),
  diffWarning: document.querySelector('#diff-warning'),
  diffContext: document.querySelector('#diff-context'),
  stageButton: document.querySelector('#stage-button'),
  stageStatus: document.querySelector('#stage-status'),
  stageProgress: document.querySelector('#stage-progress'),
  confirmDiff: document.querySelector('#confirm-diff'),
  publishButton: document.querySelector('#publish-button'),
  publishStatus: document.querySelector('#publish-status'),
  dialog: document.querySelector('#publish-dialog'),
  dialogSummary: document.querySelector('#publish-dialog-summary')
};

class ApiError extends Error {
  constructor(message, status, code, details) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code || '';
    this.details = details || null;
  }
}

function qs(selector) {
  return document.querySelector(selector);
}

function make(tag, className, textValue) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (textValue !== undefined && textValue !== null) node.textContent = String(textValue);
  return node;
}

function valueText(value, fallback) {
  if (typeof value === 'string' && value.trim()) return value;
  if (typeof value === 'number' && Number.isFinite(value)) return String(value);
  return fallback === undefined ? '—' : fallback;
}

function setStatus(node, message, tone) {
  node.textContent = message || '';
  if (tone) node.dataset.tone = tone;
  else node.removeAttribute('data-tone');
}

function formatBytes(value) {
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let amount = bytes;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return amount.toFixed(unit === 0 ? 0 : 1) + ' ' + units[unit];
}

function safeFilename(value) {
  const cleaned = String(value || 'revision')
    .replace(/[^A-Za-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return cleaned || 'revision';
}

function addAlert(message) {
  const alert = make('div', 'alert alert-error', message);
  nodes.alerts.appendChild(alert);
}

function clearAlerts() {
  nodes.alerts.replaceChildren();
}

function describeError(error) {
  if (error instanceof ApiError) {
    const code = typeof error.code === 'string' && error.code ? ' · ' + error.code : '';
    return error.message + code;
  }
  return error instanceof Error ? error.message : String(error);
}

function recordError(context, error, globalAlert) {
  const message = context + ': ' + describeError(error);
  state.apiErrors.push(message);
  if (globalAlert !== false) addAlert(message);
  nodes.apiState.textContent = 'Error de conexión';
  nodes.apiState.className = 'status-pill status-error';
}

function extractCSRF(response) {
  const headerToken = response.headers.get('X-CSRF-Token');
  if (headerToken) state.csrfToken = headerToken;
}

async function request(path, options) {
  const supplied = options || {};
  const method = String(supplied.method || 'GET').toUpperCase();
  const headers = new Headers(supplied.headers || {});
  if (!headers.has('Accept')) headers.set('Accept', 'application/json');
  if (method !== 'GET' && method !== 'HEAD' && state.csrfToken && !headers.has('X-CSRF-Token')) {
    headers.set('X-CSRF-Token', state.csrfToken);
  }

  const response = await fetch(path, {
    method: method,
    headers: headers,
    body: supplied.body,
    credentials: 'same-origin',
    cache: method === 'GET' || method === 'HEAD' ? 'no-store' : 'default'
  });
  extractCSRF(response);

  const bodyText = await response.text();
  let payload = null;
  if (bodyText) {
    try {
      payload = JSON.parse(bodyText);
    } catch (error) {
      payload = bodyText;
    }
  }

  if (!response.ok) {
    const body = payload && typeof payload === 'object' && payload.error && typeof payload.error === 'object'
      ? payload.error
      : payload;
    const code = body && typeof body === 'object'
      ? (body.code || body.error_code || '')
      : '';
    const serverMessage = body && typeof body === 'object'
      ? (body.message || body.detail || body.error_description || '')
      : '';
    const message = serverMessage || ('HTTP ' + response.status + ' en ' + path);
    throw new ApiError(message, response.status, String(code || ''), payload);
  }

  return payload;
}

function normalizeOverview(payload) {
  const root = payload && typeof payload === 'object' ? payload : {};
  const overview = root.overview && typeof root.overview === 'object' ? root.overview : root;
  const build = root.build && typeof root.build === 'object'
    ? root.build
    : (overview.build && typeof overview.build === 'object' ? overview.build : {});
  return { root: root, overview: overview, build: build };
}

function listFrom(payload, keys) {
  if (Array.isArray(payload)) return payload;
  if (!payload || typeof payload !== 'object') return [];
  for (const key of keys) {
    if (Array.isArray(payload[key])) return payload[key];
  }
  return [];
}

function profileId(profile) {
  return valueText(profile && (profile.id || profile.profile_id), '');
}

function profileName(profile) {
  return valueText(profile && (profile.display_name || profile.name || profile.profile_name), profileId(profile) || 'Perfil sin nombre');
}

function revisionId(revision) {
  if (!revision || typeof revision !== 'object') return '';
  if (typeof revision.id === 'string') return revision.id;
  if (typeof revision.revision_id === 'string') return revision.revision_id;
  if (revision.revision && typeof revision.revision.id === 'string') return revision.revision.id;
  return '';
}

function revisionSequence(revision) {
  if (!revision || typeof revision !== 'object') return null;
  const value = revision.sequence !== undefined
    ? revision.sequence
    : (revision.revision && revision.revision.sequence !== undefined ? revision.revision.sequence : null);
  const number = Number(value);
  return Number.isFinite(number) ? number : null;
}

function revisionDigest(revision) {
  if (!revision || typeof revision !== 'object') return '';
  return valueText(revision.manifest_sha256 || (revision.revision && revision.revision.manifest_sha256), '');
}

async function loadOverview() {
  clearAlerts();
  let payload;
  try {
    payload = await request('/v1/admin/ui/overview');
    state.overviewSource = '/v1/admin/ui/overview';
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 404) {
      recordError('No se pudo cargar el resumen administrativo', error, true);
      renderOverview();
      return;
    }
    try {
      payload = await request('/admin/api/overview');
      state.overviewSource = '/admin/api/overview';
    } catch (fallbackError) {
      recordError('No se pudo cargar el resumen administrativo', fallbackError, true);
      renderOverview();
      return;
    }
  }

  const normalized = normalizeOverview(payload);
  state.overview = normalized.overview;
  state.build = normalized.build;
  state.recentRevisions = listFrom(normalized.overview, ['recent_revisions', 'revisions', 'activity']);
  const overviewProfiles = listFrom(normalized.overview, ['profiles']);
  if (overviewProfiles.length || !state.profiles.length) state.profiles = overviewProfiles;

  nodes.apiState.textContent = 'Servicio disponible';
  nodes.apiState.className = 'status-pill status-ok';
  renderOverview();
  renderProfiles();
  populateParentProfiles();
  renderService();
}

async function loadProfiles() {
  try {
    const payload = await request('/v1/admin/profiles');
    const profiles = listFrom(payload, ['profiles', 'items']);
    if (profiles.length || Array.isArray(payload)) {
      state.profiles = profiles;
      renderProfiles();
      renderOverview();
      populateParentProfiles();
    }
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return;
    recordError('No se pudo cargar la lista administrativa de perfiles', error, false);
  }
}

function renderOverview() {
  renderOverviewProfiles();
  renderActivity();
}

function renderOverviewProfiles() {
  if (!state.profiles.length) {
    nodes.overviewProfiles.className = 'empty-state';
    nodes.overviewProfiles.textContent = 'No hay perfiles globales publicados.';
    return;
  }

  const list = make('div', 'profile-list');
  state.profiles.slice(0, 5).forEach(function (profile) {
    const card = make('article', 'profile-card');
    const top = make('div', 'card-top');
    const titleWrap = make('div');
    const title = make('h4', 'card-title', profileName(profile));
    const meta = make('p', 'card-meta', profileId(profile));
    titleWrap.append(title, meta);
    const badge = make('span', 'status-pill status-neutral', profile && profile.official ? 'Oficial' : 'Global');
    top.append(titleWrap, badge);
    card.appendChild(top);

    const channels = profile && profile.channels;
    if (Array.isArray(channels) && channels.length) {
      const channelText = channels.map(function (channel) {
        if (typeof channel === 'string') return channel;
        const name = valueText(channel && (channel.name || channel.channel), '');
        const currentRevision = channel && (channel.revision_id || (channel.revision && channel.revision.revision_id));
        return name && currentRevision ? name + ' → ' + currentRevision : name;
      }).filter(Boolean).join(' · ');
      if (channelText) card.appendChild(make('p', 'card-meta', 'Canales: ' + channelText));
    }

    list.appendChild(card);
  });
  nodes.overviewProfiles.className = '';
  nodes.overviewProfiles.replaceChildren(list);
}

function renderActivity() {
  if (!state.recentRevisions.length) {
    nodes.activity.className = 'empty-state';
    nodes.activity.textContent = 'No hay publicaciones recientes.';
    return;
  }

  const list = make('div', 'activity-list');
  state.recentRevisions.slice(0, 8).forEach(function (revision) {
    const item = make('article', 'activity-item');
    const name = valueText(revision.profile_name || revision.profile_id, 'Perfil');
    const sequence = revisionSequence(revision);
    const title = make('strong', '', name + (sequence !== null ? ' · versión ' + sequence : ''));
    const metaParts = [];
    if (revisionId(revision)) metaParts.push(revisionId(revision));
    if (revision.published_at || revision.created_at) metaParts.push(valueText(revision.published_at || revision.created_at));
    item.append(title, make('p', 'card-meta', metaParts.join(' · ')));
    list.appendChild(item);
  });
  nodes.activity.className = '';
  nodes.activity.replaceChildren(list);
}

function renderProfiles() {
  if (!state.profiles.length) {
    nodes.profilesList.className = 'profile-grid empty-state';
    nodes.profilesList.textContent = 'No hay perfiles globales publicados.';
    return;
  }

  const grid = make('div', 'profile-grid');
  state.profiles.forEach(function (profile) {
    const id = profileId(profile);
    const card = make('article', 'profile-card');
    const top = make('div', 'card-top');
    const titleWrap = make('div');
    titleWrap.append(
      make('h3', 'card-title', profileName(profile)),
      make('p', 'card-meta', id || 'ID no disponible')
    );
    top.append(titleWrap, make('span', 'status-pill status-neutral', profile && profile.official ? 'Oficial' : 'Global'));
    card.appendChild(top);

    const actionRow = make('div', 'card-actions');
    const historyButton = make('button', 'button button-secondary', 'Ver revisiones');
    historyButton.type = 'button';
    historyButton.disabled = !id;
    actionRow.appendChild(historyButton);
    const artworkButton = make('button', 'button button-secondary', 'Editar perfil');
    artworkButton.type = 'button';
    artworkButton.addEventListener('click', () => editProfilePresentation(profile));
    actionRow.appendChild(artworkButton);
    const deleteButton = make('button', 'button button-danger', 'Borrar perfil');
    deleteButton.type = 'button';
    deleteButton.disabled = !id;
    deleteButton.addEventListener('click', () => deleteGlobalProfile(profile));
    actionRow.appendChild(deleteButton);
    card.appendChild(actionRow);

    const revisionContainer = make('div');
    revisionContainer.hidden = true;
    card.appendChild(revisionContainer);

    historyButton.addEventListener('click', async function () {
      if (!revisionContainer.hidden) {
        revisionContainer.hidden = true;
        historyButton.textContent = 'Ver revisiones';
        return;
      }
      revisionContainer.hidden = false;
      historyButton.textContent = 'Ocultar revisiones';
      revisionContainer.className = 'revision-list';
      revisionContainer.replaceChildren(make('p', 'card-meta', 'Cargando revisiones…'));
      try {
        const revisions = await fetchProfileRevisions(id);
        renderProfileRevisions(id, revisionContainer, revisions);
      } catch (error) {
        revisionContainer.replaceChildren(make('div', 'alert alert-error', describeError(error)));
      }
    });

    grid.appendChild(card);
  });

  nodes.profilesList.className = '';
  nodes.profilesList.replaceChildren(grid);
}

async function fetchProfileRevisions(id) {
  const payload = await request('/v1/admin/profiles/' + encodeURIComponent(id) + '/revisions');
  return listFrom(payload, ['revisions', 'items']);
}

function renderProfileRevisions(profileIdValue, container, revisions) {
  if (!revisions.length) {
    container.replaceChildren(make('div', 'empty-state', 'No hay revisiones disponibles para este perfil.'));
    return;
  }

  const fragment = document.createDocumentFragment();
  revisions.forEach(function (revision) {
    const card = make('article', 'revision-card');
    const seq = revisionSequence(revision);
    const title = revisionId(revision) || 'Revisión';
    card.append(
      make('strong', '', title),
      make('p', 'card-meta', seq === null ? 'Versión no disponible' : 'Versión ' + seq)
    );
    const digest = revisionDigest(revision);
    if (digest) card.appendChild(make('p', 'card-meta', 'Manifest SHA-256: ' + digest));
    const when = revision.published_at || revision.created_at;
    if (when) card.appendChild(make('p', 'card-meta', valueText(when)));

    const revisionIdentifier = revisionId(revision);
    if (revisionIdentifier) {
      const promoteRow = make('div', 'card-actions');
      const promoteButton = make('button', 'button button-secondary', 'Fijar en canal estable');
      promoteButton.type = 'button';
      const promoteStatus = make('p', 'inline-status');
      promoteStatus.setAttribute('role', 'status');
      promoteButton.addEventListener('click', async function () {
        if (!window.confirm('¿Actualizar este perfil a la versión ' + revisionIdentifier + '? Los launchers que sigan el canal estable recibirán esta versión.')) {
          return;
        }
        promoteButton.disabled = true;
        setStatus(promoteStatus, 'Actualizando el canal estable…');
        try {
          await request('/v1/admin/profiles/' + encodeURIComponent(profileIdValue) + '/channels/stable/promote', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ revision_id: revisionIdentifier })
          });
          const updatedProfile = state.profiles.find(function (profile) {
            return profileId(profile) === profileIdValue;
          });
          if (updatedProfile) {
            const channels = Array.isArray(updatedProfile.channels) ? updatedProfile.channels : [];
            updatedProfile.channels = channels.filter(function (channel) {
              return valueText(channel && (channel.name || channel.channel), '') !== 'stable';
            });
            updatedProfile.channels.push({ name: 'stable', revision_id: revisionIdentifier });
            renderOverview();
          }
          setStatus(promoteStatus, 'Canal estable actualizado a ' + revisionIdentifier + '.', 'success');
        } catch (error) {
          setStatus(promoteStatus, describeError(error), 'error');
        } finally {
          promoteButton.disabled = false;
        }
      });
      promoteRow.appendChild(promoteButton);
      card.append(promoteRow, promoteStatus);
    }
    fragment.appendChild(card);
  });
  container.replaceChildren(fragment);
}

function populateParentProfiles() {
  const current = nodes.parentProfile.value;
  const fragment = document.createDocumentFragment();
  const root = document.createElement('option');
  root.value = '';
  root.textContent = 'Sin perfil base';
  fragment.appendChild(root);

  state.profiles.forEach(function (profile) {
    const id = profileId(profile);
    if (!id) return;
    const option = document.createElement('option');
    option.value = id;
    option.textContent = profileName(profile) + ' · ' + id;
    fragment.appendChild(option);
  });

  nodes.parentProfile.replaceChildren(fragment);
  if (current && Array.from(nodes.parentProfile.options).some(function (option) { return option.value === current; })) {
    nodes.parentProfile.value = current;
  }
}

function renderService() {
  const storage = state.overview && state.overview.storage && typeof state.overview.storage === 'object'
    ? state.overview.storage
    : {};
  qs('#service-version').textContent = valueText(state.build.version);
  qs('#service-commit').textContent = valueText(state.build.commit);
  qs('#service-revisions').textContent = valueText(storage.revision_count, '0');
  qs('#service-objects').textContent = valueText(storage.object_count, '0');
  qs('#service-bytes').textContent = formatBytes(storage.object_bytes);
  qs('#service-picker').textContent = 'webkitdirectory' in nodes.folderFallback ? 'Disponible' : 'No disponible';
  qs('#service-secure').textContent = window.isSecureContext ? 'Sí' : 'No';
  qs('#service-crypto').textContent = window.crypto && window.crypto.subtle ? 'Disponible' : 'No disponible';
}

async function checkServiceUpdate() {
  const checkButton = qs('#check-service-update');
  const applyButton = qs('#apply-service-update');
  const statusNode = qs('#service-update-status');
  checkButton.disabled = true;
  applyButton.hidden = true;
  setStatus(statusNode, 'Comprobando la versión de main…');
  try {
    const result = await request('/admin/api/service-update');
    state.serviceUpdate = result;
    if (result.update_available) {
      setStatus(statusNode, 'Disponible: ' + result.available_version + ' · ' + result.available_commit.slice(0, 12) + '.', 'success');
      applyButton.hidden = false;
    } else {
      setStatus(statusNode, 'El servicio ya está actualizado (' + result.current_version + ').', 'success');
    }
  } catch (error) {
    state.serviceUpdate = null;
    setStatus(statusNode, describeError(error), 'error');
  } finally {
    checkButton.disabled = false;
  }
}

async function applyServiceUpdate() {
  const checkButton = qs('#check-service-update');
  const applyButton = qs('#apply-service-update');
  const statusNode = qs('#service-update-status');
  const checked = state.serviceUpdate;
  if (!checked || !checked.update_available) return;

  checkButton.disabled = true;
  applyButton.disabled = true;
  setStatus(statusNode, 'Instalando ' + checked.available_version + ' y reiniciando el servicio…');
  try {
    const result = await request('/admin/api/service-update', { method: 'POST' });
    if (result.state === 'current') {
      state.serviceUpdate = result;
      setStatus(statusNode, result.message, 'success');
      applyButton.hidden = true;
      checkButton.disabled = false;
      return;
    }
    await waitForServiceRestart(checked.current_commit, checked.available_version);
    setStatus(statusNode, 'Actualización completada. El panel volverá a abrirse para iniciar sesión.', 'success');
    window.setTimeout(function () { window.location.reload(); }, 1800);
  } catch (error) {
    setStatus(statusNode, describeError(error), 'error');
    checkButton.disabled = false;
    applyButton.disabled = false;
  }
}

async function waitForServiceRestart(previousCommit, targetVersion) {
  const deadline = Date.now() + 10 * 60 * 1000;
  let delay = 3000;
  while (Date.now() < deadline) {
    await new Promise(function (resolve) { window.setTimeout(resolve, delay); });
    try {
      const current = await request('/v1/meta/version');
      if (current.commit && current.commit !== previousCommit) return;
      if (current.version === targetVersion) return;
    } catch (_) {
      // The listener is expected to disappear while systemd replaces it.
    }
    delay = Math.min(Math.round(delay * 1.5), 12000);
  }
  throw new Error('No se pudo confirmar el reinicio del servicio.');
}

function route() {
  const allowed = ['resumen', 'perfiles', 'crear', 'servicio'];
  const requested = window.location.hash.replace(/^#/, '');
  const target = allowed.includes(requested) ? requested : 'resumen';

  document.querySelectorAll('[data-view]').forEach(function (view) {
    view.hidden = view.dataset.view !== target;
  });
  document.querySelectorAll('[data-nav]').forEach(function (link) {
    if (link.dataset.nav === target) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  });

  if (!requested || requested !== target) {
    history.replaceState(null, '', '#' + target);
  }
}

function validatePath(path) {
  if (typeof path !== 'string' || !path || path.length > 1024) return false;
  if (path.startsWith('/') || path.includes('\\') || /[\u0000-\u001f\u007f]/.test(path)) return false;
  const parts = path.split('/');
  return parts.every(function (part) {
    return part && part !== '.' && part !== '..';
  });
}

function classifyPath(path) {
  const lower = path.toLowerCase();
  if (lower.startsWith('mods/') && lower.endsWith('.jar')) return 'mod';
  if (lower.startsWith('config/')) return 'config';
  return 'object';
}

function kindLabel(kind) {
  if (kind === 'mod') return 'Mod';
  if (kind === 'config') return 'Config';
  if (kind === 'object') return 'Objeto';
  return 'No compatible';
}

function defaultModId(path) {
  const name = path.split('/').pop() || 'mod';
  return 'mod_' + name.replace(/\.jar$/i, '').replace(/[^A-Za-z0-9._-]+/g, '-');
}

function defaultObjectId(path) {
  const normalized = String(path || '').replace(/\\/g, '/');
  const name = normalized.split('/').pop() || 'object';
  let hash = 2166136261;
  for (let index = 0; index < normalized.length; index += 1) {
    hash ^= normalized.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  const slug = name.replace(/[^A-Za-z0-9._-]+/g, '-').slice(0, 96) || 'object';
  return 'obj_' + slug + '-' + (hash >>> 0).toString(16).padStart(8, '0');
}

async function sha256Bytes(buffer) {
  if (!window.crypto || !window.crypto.subtle) {
    throw new Error('Web Crypto SHA-256 no está disponible en este navegador.');
  }
  const digest = await window.crypto.subtle.digest('SHA-256', buffer);
  return Array.from(new Uint8Array(digest)).map(function (byte) {
    return byte.toString(16).padStart(2, '0');
  }).join('');
}

async function sha256Text(text) {
  return sha256Bytes(new TextEncoder().encode(text));
}

async function hashFile(file) {
  return sha256Bytes(await file.arrayBuffer());
}

function fallbackPath(file) {
  const relative = typeof file.webkitRelativePath === 'string' ? file.webkitRelativePath : '';
  if (!relative) return file.name;
  const parts = relative.split('/');
  return parts.length > 1 ? parts.slice(1).join('/') : file.name;
}

async function scanRecords(records, label, mode) {
  const scanMode = mode || 'snapshot';
  const previousFiles = state.selectedFiles;
  const previousProblems = state.pathProblems;
  const nextFiles = scanMode === 'overlay' ? new Map(previousFiles) : new Map();
  const problems = [];
  const seen = new Set();

  setStatus(nodes.folderStatus, 'Leyendo y calculando SHA-256 de ' + records.length + ' archivos…');

  try {
    for (let index = 0; index < records.length; index += 1) {
      const record = records[index];
      const path = String(record.path || '');
      if (!validatePath(path)) {
        problems.push({ path: path || '(ruta vacía)', reason: 'Ruta no válida o no relativa.' });
        continue;
      }
      if (seen.has(path)) {
        problems.push({ path: path, reason: 'Ruta duplicada en la selección.' });
        continue;
      }
      seen.add(path);
      const file = record.file;
      const digest = await hashFile(file);
      const targetPath = state.pendingReplacePath || path;
      nextFiles.set(targetPath, {
        path: targetPath,
        file: file,
        sha256: digest,
        size: Number(file.size) || 0,
        mediaType: typeof file.type === 'string' ? file.type : '',
        kind: classifyPath(targetPath)
      });
      state.removedPaths.delete(targetPath);
      if (classifyPath(targetPath) === 'mod' && !state.modIds.has(targetPath)) {
        state.modIds.set(targetPath, defaultModId(targetPath));
      }
      setStatus(nodes.folderStatus, 'Procesando ' + (index + 1) + ' de ' + records.length + ' archivos…');
    }
  } catch (error) {
    state.selectedFiles = previousFiles;
    state.pathProblems = previousProblems;
    setStatus(nodes.folderStatus, 'No se pudo leer la nueva selección. Se conserva el pack anterior. ' + describeError(error), 'error');
    return;
  }

  state.selectedFiles = nextFiles;
  state.pathProblems = problems;
  state.folderSelected = true;
  state.snapshotMode = false;
  if (scanMode === 'snapshot') {
    state.removedPaths.clear();
    state.createdDirs.clear();
    state.parentMap.forEach(function (_, path) {
      if (!nextFiles.has(path)) state.removedPaths.add(path);
    });
  }
  state.pendingReplacePath = '';
  invalidatePrepared();
  await recomputeDiff();
  const total = Array.from(nextFiles.values()).reduce(function (sum, entry) { return sum + entry.size; }, 0);
  setStatus(
    nodes.folderStatus,
    label + ': ' + (scanMode === 'overlay' ? records.length + ' archivo(s) añadido(s) o reemplazado(s)' : nextFiles.size + ' archivos') + ' · ' + formatBytes(total) + (problems.length ? ' · ' + problems.length + ' rutas no compatibles' : ''),
    problems.length ? 'warning' : 'success'
  );
}

async function chooseDirectory() {
  if (!('webkitdirectory' in nodes.folderFallback)) {
    setStatus(nodes.folderStatus, 'Este navegador no permite seleccionar carpetas. Usa «Elegir archivos» o arrastra el contenido aquí.', 'error');
    return;
  }

  nodes.folderFallback.value = '';
  setStatus(nodes.folderStatus, 'Elige la carpeta raíz del modpack. Se importarán sus contenidos directamente en la raíz del perfil…');
  nodes.folderFallback.click();
}

function loadFallbackFiles(fileList) {
  const records = Array.from(fileList || []).map(function (file) {
    return { path: fallbackPath(file), file: file };
  });
  if (!records.length) {
    setStatus(nodes.folderStatus, 'No se seleccionó ninguna carpeta; se conserva la selección anterior.');
    return;
  }
  scanRecords(records, 'Carpeta importada');
}

function addFiles(fileList, replacePath, folderPath) {
  const files = Array.from(fileList || []);
  if (!files.length) return;
  if (replacePath && files.length !== 1) {
    setStatus(nodes.folderStatus, 'Para reemplazar un archivo, selecciona solo un archivo.', 'warning');
    return;
  }
  state.pendingReplacePath = replacePath || '';
  const prefix = replacePath ? '' : (folderPath === undefined ? state.pendingFolderPath : folderPath);
  const records = files.map(function (file) {
    const relative = fallbackPath(file);
    return { path: replacePath || (prefix ? prefix + '/' + relative : relative), file: file };
  });
  state.pendingFolderPath = '';
  scanRecords(records, replacePath ? 'Archivo reemplazado' : 'Archivos añadidos', 'overlay');
}

function droppedEntryRecords(entry, prefix, omitName) {
  return new Promise(function (resolve, reject) {
    if (!entry) return resolve([]);
    const path = omitName ? prefix : (prefix ? prefix + '/' + entry.name : entry.name);
    if (entry.isFile) {
      entry.file(function (file) { resolve([{ path: path, file: file }]); }, reject);
      return;
    }
    if (!entry.isDirectory) return resolve([]);
    const reader = entry.createReader();
    const all = [];
    function readBatch() {
      reader.readEntries(async function (batch) {
        if (!batch.length) return resolve(all);
        try {
          for (const child of batch) all.push.apply(all, await droppedEntryRecords(child, path, false));
          readBatch();
        } catch (error) { reject(error); }
      }, reject);
    }
    readBatch();
  });
}

async function handleExplorerDrop(event) {
  const transfer = event.dataTransfer;
  const folderRow = event.target && event.target.closest('[data-folder-path]');
  const folderPrefix = folderRow ? folderRow.dataset.folderPath : '';
  const items = Array.from(transfer && transfer.items || []);
  const entries = items.map(function (item) { return item.webkitGetAsEntry && item.webkitGetAsEntry(); }).filter(Boolean);
  if (entries.length) {
    const records = [];
    try {
      for (const entry of entries) {
        records.push.apply(records, await droppedEntryRecords(entry, folderPrefix, Boolean(entry.isDirectory)));
      }
      if (records.length) scanRecords(records, 'Archivos soltados', 'overlay');
    } catch (error) {
      setStatus(nodes.folderStatus, 'No se pudieron leer los archivos soltados: ' + describeError(error), 'error');
    }
    return;
  }
  addFiles(transfer && transfer.files, '', folderPrefix);
}

function mergedExplorerRows() {
  const rows = new Map();
  state.parentMap.forEach(function (entry, path) {
    rows.set(path, { path: path, parent: entry, local: null, status: 'inherited' });
  });
  state.selectedFiles.forEach(function (entry, path) {
    const parent = state.parentMap.get(path) || null;
    const status = !parent ? 'added' : (objectDigest(parent) === entry.sha256 ? 'inherited' : 'changed');
    rows.set(path, { path: path, parent: parent, local: entry, status: status });
  });
  state.removedPaths.forEach(function (path) {
    const parent = state.parentMap.get(path);
    if (parent) rows.set(path, { path: path, parent: parent, local: null, status: 'removed' });
  });
  if (state.snapshotMode) {
    state.parentMap.forEach(function (entry, path) {
      if (!state.selectedFiles.has(path) && !state.removedPaths.has(path)) rows.set(path, { path: path, parent: entry, local: null, status: 'removed' });
    });
  }
  return Array.from(rows.values()).sort(function (a, b) { return a.path.localeCompare(b.path); });
}

function renderExplorer() {
  const rootLabel = qs('#explorer-root-label');
  const count = qs('#explorer-count');
  const empty = qs('#explorer-empty');
  const tree = qs('#explorer-tree');
  if (!rootLabel || !tree) return;
  const hasBase = Boolean(state.parentRef);
  rootLabel.textContent = hasBase ? 'Rama de ' + state.parentRef.profile_id + ' / ' + state.parentRef.revision_id : 'Nuevo perfil · vacío';
  const rows = mergedExplorerRows();
  const folderBlockers = directoryRemovalBlockers(rows);
  const query = (qs('#explorer-search').value || '').trim().toLocaleLowerCase();
  const filtered = rows.filter(function (row) { return row.path.toLocaleLowerCase().includes(query); });
  count.textContent = rows.length + (rows.length === 1 ? ' archivo' : ' archivos');
  empty.hidden = filtered.length > 0;
  empty.textContent = query ? 'No hay archivos que coincidan con el filtro.' : (hasBase ? 'El perfil base está vacío.' : 'Arrastra archivos aquí o pulsa «Añadir archivos».');
  tree.replaceChildren();
  const root = { name: '', path: '', dirs: new Map(), files: [] };
  function ensureDirectory(path) {
    let current = root;
    let currentPath = '';
    path.split('/').filter(Boolean).forEach(function (part) {
      currentPath = currentPath ? currentPath + '/' + part : part;
      if (!current.dirs.has(part)) current.dirs.set(part, { name: part, path: currentPath, dirs: new Map(), files: [] });
      current = current.dirs.get(part);
    });
    return current;
  }
  state.createdDirs.forEach(function (path) {
    if (!query || path.toLocaleLowerCase().includes(query) || filtered.some(function (entry) { return entry.path.startsWith(path + '/'); })) ensureDirectory(path);
  });
  filtered.forEach(function (entry) {
    const parts = entry.path.split('/');
    const fileName = parts.pop();
    const parent = parts.length ? ensureDirectory(parts.join('/')) : root;
    parent.files.push({ ...entry, fileName: fileName });
  });

  function smallAction(parent, title, symbol, handler, tone) {
    const button = make('button', 'tree-action' + (tone ? ' tree-action-' + tone : ''), symbol);
    button.type = 'button';
    button.title = title;
    button.setAttribute('aria-label', title + (parent.path ? ' en ' + parent.path : ''));
    button.addEventListener('click', handler);
    return button;
  }

  function renderFile(entry, host) {
    const row = make('div', 'explorer-row explorer-' + entry.status);
    row.setAttribute('role', 'listitem');
    const identity = make('div', 'explorer-file');
    identity.appendChild(make('span', 'explorer-file-icon', entry.path.startsWith('mods/') ? '◆' : '▤'));
    const path = make('code', 'explorer-path', entry.fileName);
    path.title = entry.path;
    identity.append(path, make('span', 'explorer-file-meta', kindLabel((entry.local || entry.parent || {}).kind) + ' · ' + formatBytes(entry.local ? entry.local.size : Number((entry.parent && entry.parent.object && entry.parent.object.size) || 0))));
    const stateLabel = entry.status === 'inherited' ? 'Heredado' : entry.status === 'added' ? 'Añadido' : entry.status === 'changed' ? 'Modificado' : 'Excluido';
    const actions = make('div', 'explorer-actions');
    actions.appendChild(make('span', 'change-badge change-' + (entry.status === 'inherited' ? 'unchanged' : entry.status), stateLabel));
    if (entry.status === 'removed') {
      actions.appendChild(smallAction(entry, 'Restaurar archivo', '↶', function () { restorePath(entry.path); }));
    } else {
      actions.appendChild(smallAction(entry, 'Descargar archivo', '↓', function () { downloadExplorerFile(entry); }));
      actions.appendChild(smallAction(entry, 'Reemplazar archivo', '↻', function () { state.pendingReplacePath = entry.path; state.pendingFolderPath = ''; nodes.filePicker.click(); }));
      if (entry.local && entry.local.file && isEditableText(entry.path, entry.local.mediaType)) {
        actions.appendChild(smallAction(entry, isConfigRuleFile(entry.path, entry.local.kind) ? 'Editar archivo y reglas' : 'Editar archivo de texto', '✎', function () { editLocalFile(entry.path); }));
      } else if (entry.parent && isEditableText(entry.path, (entry.parent.object || {}).media_type)) {
        actions.appendChild(smallAction(entry, isConfigRuleFile(entry.path, entry.parent.kind) ? 'Editar archivo y reglas' : 'Editar archivo de texto', '✎', function () { editInheritedFile(entry.path, entry.parent); }));
      }
      const canRemove = !hasBase || (entry.parent && (entry.parent.kind === 'mod' || entry.parent.kind === 'config')) || entry.local;
      const remove = smallAction(entry, canRemove ? 'Quitar de esta rama' : 'No se puede quitar este tipo de archivo', '×', function () { removePath(entry.path); }, 'danger');
      remove.disabled = !canRemove;
      actions.appendChild(remove);
    }
    row.append(identity, actions);
    host.appendChild(row);
  }

  function renderDirectory(directory, host) {
    const branch = make('div', 'tree-branch');
    branch.dataset.folderPath = directory.path;
    const folderRow = make('div', 'tree-folder-row');
    folderRow.dataset.folderPath = directory.path;
    const collapsed = !query && state.collapsedDirs.has(directory.path);
    const toggle = make('button', 'tree-folder-toggle', collapsed ? '▸' : '▾');
    toggle.type = 'button';
    toggle.setAttribute('aria-expanded', String(!collapsed));
    toggle.setAttribute('aria-label', (collapsed ? 'Expandir ' : 'Contraer ') + directory.path);
    toggle.addEventListener('click', function () {
      if (state.collapsedDirs.has(directory.path)) state.collapsedDirs.delete(directory.path);
      else state.collapsedDirs.add(directory.path);
      renderExplorer();
    });
    folderRow.append(toggle, make('span', 'tree-folder-icon', '▰'), make('strong', 'tree-folder-name', directory.name));
    const folderActions = make('div', 'tree-folder-actions');
    folderActions.appendChild(smallAction(directory, 'Añadir archivos a esta carpeta', '+', function () {
      state.pendingReplacePath = '';
      state.pendingFolderPath = directory.path;
      nodes.filePicker.click();
    }));
    folderActions.appendChild(smallAction(directory, 'Crear subcarpeta', '▱+', function () { openCreateFolder(directory.path); }));
    const blockedPath = folderBlockers.get(directory.path) || '';
    const removeFolder = smallAction(
      directory,
      blockedPath ? 'Esta rama no puede retirar el archivo heredado ' + blockedPath : 'Borrar esta carpeta y todo su contenido',
      '×',
      function () { removeDirectory(directory.path); },
      'danger'
    );
    removeFolder.disabled = Boolean(blockedPath);
    folderActions.appendChild(removeFolder);
    folderRow.appendChild(folderActions);
    branch.appendChild(folderRow);
    if (!collapsed) {
      const children = make('div', 'tree-children');
      Array.from(directory.dirs.values()).sort(function (a, b) { return a.name.localeCompare(b.name); }).forEach(function (child) { renderDirectory(child, children); });
      directory.files.sort(function (a, b) { return a.fileName.localeCompare(b.fileName); }).forEach(function (entry) { renderFile(entry, children); });
      branch.appendChild(children);
    }
    host.appendChild(branch);
  }

  Array.from(root.dirs.values()).sort(function (a, b) { return a.name.localeCompare(b.name); }).forEach(function (directory) { renderDirectory(directory, tree); });
  root.files.sort(function (a, b) { return a.fileName.localeCompare(b.fileName); }).forEach(function (entry) { renderFile(entry, tree); });
}

function openCreateFolder(parentPath) {
  state.pendingNewFolderParent = parentPath || '';
  qs('#create-folder-parent').textContent = parentPath ? 'Dentro de ' + parentPath : 'En la raíz del perfil.';
  qs('#create-folder-name').value = '';
  qs('#create-folder-dialog').showModal();
  qs('#create-folder-name').focus();
}

function createFolderFromDialog() {
  const name = qs('#create-folder-name').value.trim();
  if (!name || name.includes('/') || name.includes('\\') || name === '.' || name === '..') {
    setStatus(nodes.folderStatus, 'Escribe un nombre de carpeta válido, sin separadores.', 'error');
    return;
  }
  const path = state.pendingNewFolderParent ? state.pendingNewFolderParent + '/' + name : name;
  if (!validatePath(path + '/entry')) {
    setStatus(nodes.folderStatus, 'La ruta de la carpeta no es válida.', 'error');
    return;
  }
  state.createdDirs.add(path);
  state.collapsedDirs.delete(path);
  qs('#create-folder-dialog').close('save');
  renderExplorer();
  setStatus(nodes.folderStatus, 'Carpeta creada. Añade archivos para incluirla en la publicación.', 'success');
}

async function downloadExplorerFile(entry) {
  try {
    let blob;
    if (entry.local && entry.local.file) {
      blob = entry.local.file;
    } else {
      const digest = objectDigest(entry.parent);
      if (!digest) throw new Error('No hay una huella publicada para descargar este archivo.');
      const response = await fetch('/v1/objects/sha256/' + encodeURIComponent(digest), { credentials: 'same-origin' });
      if (!response.ok) throw new Error('HTTP ' + response.status);
      blob = await response.blob();
    }
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = entry.path.split('/').pop();
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    // Let the browser finish reading the blob before revoking its object URL.
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (error) {
    setStatus(nodes.folderStatus, 'No se pudo descargar el archivo: ' + describeError(error), 'error');
  }
}

function isEditableText(path, mediaType) {
  return /\.(txt|json|toml|cfg|ini|properties|yml|yaml|xml|md|mcmeta|conf|lang)$/i.test(path) || /^(text\/|application\/(json|xml|toml))/i.test(mediaType || '');
}

function configRuleFormat(path, kind) {
  if (kind !== 'config') return '';
  if (/\.toml$/i.test(path)) return 'toml';
  if (/\.properties$/i.test(path)) return 'properties';
  if (/\.txt$/i.test(path)) return 'text_lines';
  return '';
}
function isConfigRuleFile(path, kind) { return Boolean(configRuleFormat(path, kind)); }

function removePath(path) {
  const parent = state.parentMap.get(path);
  if (state.parentRef && parent && parent.kind !== 'mod' && parent.kind !== 'config') return;
  state.selectedFiles.delete(path);
  if (state.parentRef && parent) state.removedPaths.add(path);
  else state.removedPaths.delete(path);
  state.folderSelected = true;
  invalidatePrepared();
  recomputeDiff();
}

function directoryRemovalBlockers(rows) {
  const blockers = new Map();
  if (!state.parentRef) return blockers;
  rows.forEach(function (entry) {
    if (entry.status === 'removed') return;
    const parent = state.parentMap.get(entry.path);
    if (!parent || parent.kind === 'mod' || parent.kind === 'config') return;
    let separator = entry.path.lastIndexOf('/');
    while (separator > 0) {
      const directoryPath = entry.path.slice(0, separator);
      if (!blockers.has(directoryPath)) blockers.set(directoryPath, entry.path);
      separator = entry.path.lastIndexOf('/', separator - 1);
    }
  });
  return blockers;
}

function directoryRemovalBlockPath(path) {
  return directoryRemovalBlockers(mergedExplorerRows()).get(path) || '';
}

async function removeDirectory(path) {
  const blockedPath = directoryRemovalBlockPath(path);
  if (blockedPath) {
    setStatus(nodes.folderStatus, 'Esta rama no puede retirar el archivo heredado ' + blockedPath + '.', 'error');
    return;
  }

  const prefix = path + '/';
  for (const filePath of Array.from(state.selectedFiles.keys())) {
    if (!filePath.startsWith(prefix)) continue;
    state.selectedFiles.delete(filePath);
  }
  for (const filePath of state.parentMap.keys()) {
    if (!filePath.startsWith(prefix)) continue;
    if (state.parentRef) {
      state.removedPaths.add(filePath);
    } else {
      state.removedPaths.delete(filePath);
    }
  }
  for (const directoryPath of Array.from(state.createdDirs)) {
    if (directoryPath === path || directoryPath.startsWith(prefix)) state.createdDirs.delete(directoryPath);
  }
  state.folderSelected = true;
  invalidatePrepared();
  await recomputeDiff();
  setStatus(nodes.folderStatus, 'Carpeta «' + path + '» y todo su contenido se han quitado de esta rama.', 'success');
}

function restorePath(path) {
  state.removedPaths.delete(path);
  invalidatePrepared();
  recomputeDiff();
}

function editLocalFile(path) {
  const entry = state.selectedFiles.get(path);
  if (!entry || !entry.file) return;
  entry.file.text().then(function (content) { showFileEditor(path, content); }).catch(function (error) {
    setStatus(nodes.folderStatus, 'No se pudo abrir el archivo: ' + describeError(error), 'error');
  });
}

async function editInheritedFile(path, entry) {
  const digest = objectDigest(entry);
  if (!digest) return;
  try {
    const response = await fetch('/v1/objects/sha256/' + encodeURIComponent(digest), { credentials: 'same-origin' });
    if (!response.ok) throw new Error('HTTP ' + response.status);
    const bytes = await response.arrayBuffer();
    if (bytes.byteLength > 1024 * 1024) throw new Error('El editor integrado admite archivos de hasta 1 MiB. Descárgalo y reemplázalo desde tu equipo.');
    const content = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
    showFileEditor(path, content);
  } catch (error) {
    setStatus(nodes.folderStatus, 'No se pudo editar el archivo heredado: ' + describeError(error), 'error');
  }
}

function showFileEditor(path, content) {
  state.editorPath = path;
  const format = configRuleFormat(path, (state.parentMap.get(path) || {}).kind || (state.selectedFiles.get(path) || {}).kind);
  qs('#file-editor-title').textContent = format ? 'Editar archivo de configuración' : 'Editar archivo';
  qs('#file-editor-path').textContent = path;
  qs('#file-editor-content').value = content;
  qs('#toml-rule-editor').hidden = !format;
  qs('#file-editor').classList.toggle('config-editing', Boolean(format));
  if (format) renderConfigRuleEditor();
  qs('#file-editor').showModal();
}

function saveEditedFile() {
  const path = state.editorPath;
  if (!path) return;
  if (state.localConfigSettings.size) syncConfigRulesFromEditor();
  const parent = state.parentMap.get(path);
  const mediaType = (parent && parent.object && parent.object.media_type) || 'text/plain';
  const file = new File([qs('#file-editor-content').value], path.split('/').pop(), { type: mediaType });
  state.pendingReplacePath = path;
  addFiles([file], path);
  qs('#file-editor').close('save');
  state.editorPath = '';
}

function syntheticRecords(variant) {
  if (variant === 'child') {
    return [
      { path: 'mods/bootoptim-synthetic.jar', file: new File(['synthetic-mod-v2'], 'bootoptim-synthetic.jar', { type: 'application/java-archive' }) },
      { path: 'config/bootoptim-synthetic.toml', file: new File(['enabled=true\nlevel=2\n'], 'bootoptim-synthetic.toml', { type: 'text/plain' }) },
      { path: 'config/bootoptim-synthetic.properties', file: new File(['feature.enabled=false\nmenu.label=Child profile\n'], 'bootoptim-synthetic.properties', { type: 'text/plain' }) },
      { path: 'config/bootoptim-synthetic.txt', file: new File(['Synthetic child profile\nKeep this line\n'], 'bootoptim-synthetic.txt', { type: 'text/plain' }) },
      { path: 'resourcepacks/bootoptim-synthetic/pack.mcmeta', file: new File(['{"pack":{"description":"synthetic v2","pack_format":1}}'], 'pack.mcmeta', { type: 'application/json' }) },
      { path: 'resourcepacks/bootoptim-synthetic/credits.txt', file: new File(['synthetic child fixture\n'], 'credits.txt', { type: 'text/plain' }) }
    ];
  }

  return [
    { path: 'mods/bootoptim-synthetic.jar', file: new File(['synthetic-mod-v1'], 'bootoptim-synthetic.jar', { type: 'application/java-archive' }) },
    { path: 'mods/remove-me.jar', file: new File(['synthetic-removal-target-v1'], 'remove-me.jar', { type: 'application/java-archive' }) },
    { path: 'config/bootoptim-synthetic.toml', file: new File(['enabled=true\nlevel=1\n'], 'bootoptim-synthetic.toml', { type: 'text/plain' }) },
    { path: 'config/bootoptim-synthetic.properties', file: new File(['feature.enabled=true\nmenu.label=Root profile\n'], 'bootoptim-synthetic.properties', { type: 'text/plain' }) },
    { path: 'config/bootoptim-synthetic.txt', file: new File(['Synthetic root profile\nKeep this line\n'], 'bootoptim-synthetic.txt', { type: 'text/plain' }) },
    { path: 'resourcepacks/bootoptim-synthetic/pack.mcmeta', file: new File(['{"pack":{"description":"synthetic root","pack_format":1}}'], 'pack.mcmeta', { type: 'application/json' }) }
  ];
}

async function loadSyntheticPack() {
  let variant = 'root';
  if (state.parentRef) {
    if (!state.parentMap.has('mods/bootoptim-synthetic.jar') || !state.parentMap.has('mods/remove-me.jar')) {
      setStatus(nodes.folderStatus, 'El ejemplo necesita un perfil base creado con otro ejemplo. Selecciona “Sin perfil base” para empezar desde cero.', 'warning');
      return;
    }
    variant = 'child';
  }
  await scanRecords(syntheticRecords(variant), variant === 'child' ? 'Ejemplo derivado' : 'Ejemplo inicial');
}

function objectDigest(entry) {
  return entry && entry.object && typeof entry.object.sha256 === 'string' ? entry.object.sha256 : '';
}

function applyManifest(map, manifest, configSettings) {
  const removeMods = Array.isArray(manifest.remove_mods) ? manifest.remove_mods : [];
  removeMods.forEach(function (removal) {
    for (const pair of map.entries()) {
      const path = pair[0];
      const entry = pair[1];
      if (entry.kind === 'mod' && entry.id === removal.id) {
        map.delete(path);
        break;
      }
    }
  });

  const removeConfigs = Array.isArray(manifest.remove_configs) ? manifest.remove_configs : [];
  removeConfigs.forEach(function (removal) {
    if (typeof removal.path === 'string') {
      map.delete(removal.path);
      for (const id of configSettings.keys()) {
        if (id.startsWith(removal.path + '\0')) configSettings.delete(id);
      }
    }
  });

  const mods = Array.isArray(manifest.mods) ? manifest.mods : [];
  mods.forEach(function (entry) {
    if (entry && typeof entry.path === 'string') {
      map.set(entry.path, {
        kind: 'mod',
        id: valueText(entry.id, defaultModId(entry.path)),
        path: entry.path,
        object: entry.object || {},
        policy: ''
      });
    }
  });

  const configs = Array.isArray(manifest.configs) ? manifest.configs : [];
  configs.forEach(function (entry) {
    if (entry && typeof entry.path === 'string') {
      map.set(entry.path, {
        kind: 'config',
        id: '',
        path: entry.path,
        object: entry.object || {},
        policy: valueText(entry.policy, 'default_once')
      });
    }
  });

  const objects = Array.isArray(manifest.objects) ? manifest.objects : [];
  objects.forEach(function (entry) {
    if (entry && typeof entry.path === 'string') {
      map.set(entry.path, {
        kind: 'object',
        id: valueText(entry.id, entry.path),
        path: entry.path,
        object: entry.object || {},
        policy: ''
      });
    }
  });

  (Array.isArray(manifest.config_settings) ? manifest.config_settings : []).forEach(function (rule) {
    if (!rule || typeof rule.path !== 'string' || typeof rule.key !== 'string') return;
    const id = rule.path + '\0' + rule.key;
    configSettings.set(id, {
      path: rule.path,
      format: rule.format,
      key: rule.key,
      value: rule.value,
      policy: rule.policy === 'enforced' ? 'enforced' : 'default_once'
    });
  });
}

function unwrapEnvelope(payload) {
  if (!payload || typeof payload !== 'object') return null;
  if (payload.envelope && typeof payload.envelope === 'object') return payload.envelope;
  if (payload.signed_revision && typeof payload.signed_revision === 'object') return payload.signed_revision;
  if (payload.revision && payload.revision.manifest) return payload.revision;
  return payload;
}

function parseManifest(envelope) {
  if (!envelope || envelope.manifest === undefined) return null;
  if (typeof envelope.manifest === 'string') {
    try {
      return JSON.parse(envelope.manifest);
    } catch (error) {
      throw new Error('El manifest de la revisión madre no contiene JSON válido.');
    }
  }
  return envelope.manifest;
}

async function fetchRevision(profile, revision) {
  return request('/v1/profiles/' + encodeURIComponent(profile) + '/revisions/' + encodeURIComponent(revision));
}

async function resolveEffective(profile, revision, depth, seen) {
  if (depth > MAX_INHERITANCE_LEVELS) throw new Error('La cadena supera el límite fijo de 8 niveles heredados.');
  const key = profile + '\n' + revision;
  if (seen.has(key)) throw new Error('Se detectó un ciclo en la cadena de revisiones madre.');
  seen.add(key);

  const payload = await fetchRevision(profile, revision);
  const envelope = unwrapEnvelope(payload);
  const manifest = parseManifest(envelope);
  if (!manifest || typeof manifest !== 'object') throw new Error('La revisión madre no expone un manifest utilizable.');

  const canonical = canonicalize(manifest);
  const computedDigest = await sha256Text(canonical);
  const reportedDigest = valueText(envelope.manifest_sha256 || (payload && payload.manifest_sha256), '');
  if (reportedDigest && reportedDigest !== computedDigest) {
    throw new Error('El SHA-256 publicado de la revisión madre no coincide con su manifest.');
  }

  let map = new Map();
  let configSettings = new Map();
  let inheritanceDepth = 0;
  if (manifest.base) {
    const base = manifest.base;
    const baseProfile = valueText(base.profile_id, '');
    const baseRevision = valueText(base.revision_id, '');
    const baseDigest = valueText(base.manifest_sha256, '');
    if (!baseProfile || !baseRevision || !baseDigest) throw new Error('La referencia madre publicada está incompleta.');

    const inherited = await resolveEffective(baseProfile, baseRevision, depth + 1, seen);
    if (inherited.ref.manifest_sha256 !== baseDigest) {
      throw new Error('La referencia madre fijada no coincide con el manifest resuelto.');
    }
    map = new Map(inherited.map);
    configSettings = new Map(inherited.configSettings);
    inheritanceDepth = inherited.inheritanceDepth + 1;
  }

  applyManifest(map, manifest, configSettings);
  seen.delete(key);

  const manifestProfile = manifest.profile && valueText(manifest.profile.id, profile);
  const manifestRevision = manifest.revision && valueText(manifest.revision.id, revision);
  return {
    map: map,
    configSettings: configSettings,
    manifest: manifest,
    inheritanceDepth: inheritanceDepth,
    ref: {
      profile_id: manifestProfile,
      revision_id: manifestRevision,
      manifest_sha256: computedDigest
    },
    sequence: manifest.revision ? Number(manifest.revision.sequence) : null
  };
}

async function handleParentProfileChange() {
  invalidatePrepared();
  state.parentMap = new Map();
  state.parentConfigSettings = new Map();
  state.localConfigSettings.clear();
  state.parentRef = null;
  state.parentDepth = 0;
  state.parentManifest = null;
  state.removedPaths.clear();

  const id = nodes.parentProfile.value;
  nodes.parentRevision.disabled = !id;
  if (!id) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'Selecciona un perfil primero';
    nodes.parentRevision.replaceChildren(option);
    setStatus(nodes.parentStatus, 'Sin perfil base: este perfil empieza en su versión 1 y se mantiene independiente.');
    nodes.syntheticHint.textContent = 'Previsualiza el flujo con archivos de ejemplo antes de elegir una carpeta.';
    setStatus(nodes.folderStatus, state.selectedFiles.size
      ? 'Perfil independiente: ' + state.selectedFiles.size + ' archivo(s) propios.'
      : 'Perfil vacío. Añade archivos o arrástralos aquí.');
    await recomputeDiff();
    return;
  }

    setStatus(nodes.parentStatus, 'Cargando versiones de ' + id + '…');
  try {
    const revisions = await fetchProfileRevisions(id);
    const fragment = document.createDocumentFragment();
    const placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.textContent = 'Selecciona una versión';
    fragment.appendChild(placeholder);
    revisions.sort(function (a, b) { return (revisionSequence(b) || 0) - (revisionSequence(a) || 0); });
    revisions.forEach(function (revision, index) {
      const idValue = revisionId(revision);
      if (!idValue) return;
      const option = document.createElement('option');
      option.value = idValue;
      const seq = revisionSequence(revision);
      option.textContent = (index === 0 ? 'Más reciente · ' : '') + idValue + (seq === null ? '' : ' · versión ' + seq);
      fragment.appendChild(option);
    });
    nodes.parentRevision.replaceChildren(fragment);
    if (revisions.length) {
      const latestID = revisionId(revisions[0]);
      if (latestID) {
        nodes.parentRevision.value = latestID;
        await handleParentRevisionChange();
        return;
      }
    }
    setStatus(nodes.parentStatus, 'Este perfil todavía no tiene versiones disponibles.', 'warning');
  } catch (error) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'No se pudieron cargar revisiones';
    nodes.parentRevision.replaceChildren(option);
    nodes.parentRevision.disabled = true;
    setStatus(nodes.parentStatus, 'No se pudieron cargar las versiones: ' + describeError(error), 'error');
  }
  await recomputeDiff();
}

async function handleParentRevisionChange() {
  invalidatePrepared();
  state.parentMap = new Map();
  state.parentConfigSettings = new Map();
  state.localConfigSettings.clear();
  state.parentRef = null;
  state.parentDepth = 0;
  state.parentManifest = null;
  state.removedPaths.clear();

  const profile = nodes.parentProfile.value;
  const revision = nodes.parentRevision.value;
  if (!profile || !revision) {
    setStatus(nodes.parentStatus, 'Selecciona una versión base.', 'warning');
    await recomputeDiff();
    return;
  }

  setStatus(nodes.parentStatus, 'Cargando los archivos de la versión base…');
  try {
    const resolved = await resolveEffective(profile, revision, 0, new Set());
    state.parentMap = resolved.map;
    state.parentConfigSettings = resolved.configSettings;
    state.parentRef = resolved.ref;
    state.parentDepth = resolved.inheritanceDepth;
    state.parentManifest = resolved.manifest;
    setStatus(nodes.folderStatus, resolved.map.size + (resolved.map.size === 1
      ? ' archivo heredado listo para esta rama.'
      : ' archivos heredados listos para esta rama.'));
    const selectedOption = nodes.parentRevision.options[nodes.parentRevision.selectedIndex];
    const isNewest = selectedOption && selectedOption.textContent.startsWith('Más reciente · ');
    if (resolved.inheritanceDepth >= MAX_INHERITANCE_LEVELS) {
      setStatus(nodes.parentStatus, 'No se puede crear otro perfil derivado: esta base ya tiene 8 niveles heredados. El máximo permitido es 8.', 'error');
    } else {
      setStatus(nodes.parentStatus, isNewest
        ? 'Base: ' + resolved.ref.profile_id + ' / ' + resolved.ref.revision_id + '. La revisión queda fijada; el seguimiento automático de futuras versiones aún no está disponible.'
        : 'Base histórica: ' + resolved.ref.profile_id + ' / ' + resolved.ref.revision_id + '. Esta publicación queda fijada a esa revisión y no heredará cambios posteriores.', 'warning');
    }
    nodes.syntheticHint.textContent = state.parentMap.has('mods/bootoptim-synthetic.jar')
      ? 'El ejemplo mostrará archivos añadidos, modificados y eliminados respecto al perfil base.'
      : 'Para comparar ejemplos, el perfil base debe haberse creado con el botón «Cargar ejemplo».';
  } catch (error) {
    setStatus(nodes.parentStatus, 'No se pudo cargar la versión base: ' + describeError(error), 'error');
  }
  await recomputeDiff();
}

function invalidatePrepared() {
  state.manifest = null;
  state.canonicalManifest = '';
  state.manifestSHA256 = '';
  state.staged = false;
  nodes.publishButton.disabled = true;
  nodes.confirmDiff.checked = false;
  setStatus(nodes.publishStatus, '');
}

async function recomputeDiff() {
  if (!state.folderSelected) {
    state.diff = [];
    renderDiff();
    return;
  }

  const diff = [];
  state.pathProblems.forEach(function (problem) {
    diff.push({
      status: 'unsupported',
      path: problem.path,
      kind: 'unsupported',
      size: 0,
      reason: problem.reason
    });
  });

  state.selectedFiles.forEach(function (local, path) {
    const parent = state.parentMap.get(path);
    if (!parent) {
      diff.push({
        status: 'added',
        path: path,
        kind: local.kind,
        size: local.size,
        local: local,
        parent: null
      });
      return;
    }

    const parentDigest = objectDigest(parent);
    if (parentDigest !== local.sha256) {
      diff.push({
        status: 'changed',
        path: path,
        kind: parent.kind || local.kind,
        size: local.size,
        local: local,
        parent: parent
      });
    }
  });

  state.parentMap.forEach(function (parent, path) {
    if (state.selectedFiles.has(path) || (!state.snapshotMode && !state.removedPaths.has(path))) return;
    if (parent.kind === 'mod' || parent.kind === 'config') {
      diff.push({
        status: 'removed',
        path: path,
        kind: parent.kind,
        size: 0,
        local: null,
        parent: parent
      });
    } else {
      diff.push({
        status: 'unsupported',
        path: path,
        kind: 'unsupported',
        size: 0,
        local: null,
        parent: parent,
        reason: 'El manifest v1 no define eliminación genérica de objetos para esta ruta.'
      });
    }
  });

  diff.sort(function (a, b) { return a.path.localeCompare(b.path); });
  state.diff = diff;
  renderDiff();
}

function changeLabel(status) {
  if (status === 'added') return 'Alta';
  if (status === 'changed') return 'Cambio';
  if (status === 'removed') return 'Eliminación';
  return 'No compatible';
}

function policyFor(entry) {
  if (state.policies.has(entry.path)) return state.policies.get(entry.path);
  if (entry.parent && entry.parent.policy) return entry.parent.policy;
  return 'default_once';
}

function configRuleID(path, key) { return path + '\0' + key; }

function stripTomlComment(line) {
  let quote = '';
  let escaped = false;
  for (let index = 0; index < line.length; index++) {
    const character = line[index];
    if (quote === '"') {
      if (escaped) escaped = false;
      else if (character === '\\') escaped = true;
      else if (character === '"') quote = '';
    } else if (quote === "'") {
      if (character === "'") quote = '';
    } else if (character === '"' || character === "'") quote = character;
    else if (character === '#') return line.slice(0, index);
  }
  return line;
}

function parseTomlEditableEntries(path, text) {
  const entries = [];
  let section = '';
  text.split(/\r?\n/).forEach(function (line, index) {
    const trimmed = line.trim();
    const table = trimmed.match(/^\[([A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*)\]$/);
    if (table) { section = table[1]; return; }
    const assignment = stripTomlComment(line).match(/^\s*([A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*)\s*=\s*(.*?)\s*$/);
    if (!assignment) return;
    const raw = assignment[2].trim();
    let value;
    try {
      if (/^(true|false)$/.test(raw)) value = raw === 'true';
      else if (/^[+-]?(?:\d[\d_]*)(?:\.[\d_]+)?(?:[eE][+-]?\d+)?$/.test(raw)) value = Number(raw.replace(/_/g, ''));
      else if (/^"(?:[^"\\]|\\.)*"$/.test(raw)) value = JSON.parse(raw);
      else if (/^\[.*\]$/.test(raw)) {
        const jsonish = raw.replace(/,\s*\]$/, ']').replace(/\s*,\s*/g, ',');
        value = JSON.parse(jsonish);
      } else if (/^'[^']*'$/.test(raw)) value = raw.slice(1, -1);
      else return;
    } catch (_) { return; }
    if (value === null || (typeof value === 'number' && !Number.isFinite(value)) || (!Array.isArray(value) && !['string', 'number', 'boolean'].includes(typeof value)) ||
      (Array.isArray(value) && !value.every(function (item) { return item !== null && ['string', 'number', 'boolean'].includes(typeof item); }))) return;
    const key = section ? section + '.' + assignment[1] : assignment[1];
    if (key.length <= 256) entries.push({ path: path, key: key, value: value, line: index + 1 });
  });
  return entries;
}

function unescapePropertiesValue(value) {
  return value.replace(/\\(u[0-9a-fA-F]{4}|.)/g, function (_, escaped) {
    if (escaped[0] === 'u' && escaped.length === 5) return String.fromCharCode(parseInt(escaped.slice(1), 16));
    return ({ t: '\t', n: '\n', r: '\r', f: '\f' })[escaped] || escaped;
  });
}

function parsePropertiesEntries(path, text) {
  const values = new Map();
  text.split(/\r?\n/).forEach(function (line, index) {
    const indent = line.length - line.trimStart().length;
    const content = line.slice(indent);
    if (!content || content[0] === '#' || content[0] === '!') return;
    let split = -1;
    let escaped = false;
    for (let i = 0; i < content.length; i++) {
      const c = content[i];
      if (escaped) { escaped = false; continue; }
      if (c === '\\') { escaped = true; continue; }
      if (c === '=' || c === ':' || /\s/.test(c)) { split = i; break; }
    }
    const key = (split < 0 ? content : content.slice(0, split)).trim();
    if (!/^[A-Za-z0-9_.-]{1,256}$/.test(key)) return;
    let valueStart = split < 0 ? content.length : split;
    while (valueStart < content.length && /\s/.test(content[valueStart])) valueStart++;
    if (content[valueStart] === '=' || content[valueStart] === ':') valueStart++;
    while (valueStart < content.length && /\s/.test(content[valueStart])) valueStart++;
    values.set(key, { path: path, key: key, format: 'properties', value: unescapePropertiesValue(content.slice(valueStart)), line: index + 1, label: key });
  });
  return Array.from(values.values());
}

function parseTextLineEntries(path, text) {
  if (!text) return [];
  const lines = text.split(/\r?\n/);
  if (text.endsWith('\n')) lines.pop();
  return lines.flatMap(function (line, index) {
    return [{ path: path, key: 'line:' + (index + 1), format: 'text_lines', value: line, line: index + 1, label: 'Línea ' + (index + 1) + (line.trim() ? '' : ' · vacía') }];
  });
}

function parseConfigRuleEntries(path, text, kind) {
  const format = configRuleFormat(path, kind);
  if (format === 'toml') return parseTomlEditableEntries(path, text).map(function (entry) { return { ...entry, format: 'toml', label: entry.key }; });
  if (format === 'properties') return parsePropertiesEntries(path, text);
  if (format === 'text_lines') return parseTextLineEntries(path, text);
  return [];
}

function canOverrideConfigRule(id) {
  if (!state.parentConfigSettings.has(id)) return true;
  const permissions = state.parentManifest && state.parentManifest.permissions && state.parentManifest.permissions.configs;
  const inherited = state.parentConfigSettings.get(id);
  return Boolean(permissions && (inherited.policy === 'enforced' ? permissions.override_enforced : permissions.override_default_once));
}

function renderConfigRuleEditor() {
  const path = state.editorPath;
  const rows = qs('#toml-rule-rows');
  const kind = (state.parentMap.get(path) || {}).kind || (state.selectedFiles.get(path) || {}).kind;
  const format = configRuleFormat(path, kind);
  const entries = parseConfigRuleEntries(path, qs('#file-editor-content').value, kind).filter(function (entry) {
    if (entry.format !== 'text_lines' || entry.value.trim()) return true;
    const id = configRuleID(path, entry.key);
    return state.parentConfigSettings.has(id) || state.localConfigSettings.has(id);
  });
  const fragment = document.createDocumentFragment();
  entries.forEach(function (entry) {
    const id = configRuleID(path, entry.key);
    const inherited = state.parentConfigSettings.get(id);
    const local = state.localConfigSettings.get(id);
    const canOverride = canOverrideConfigRule(id);
    const row = make('div', 'config-rule-row');
    const identity = make('div', 'config-rule-identity');
    identity.append(make('code', '', entry.label || entry.key), make('span', 'config-rule-current', JSON.stringify(entry.value)));
    const control = document.createElement('select');
    control.setAttribute('aria-label', 'Regla de ' + entry.key);
    const options = inherited
      ? [['inherit', 'Heredada'], ['enforced', 'Obligatoria'], ['default_once', 'Valor inicial']]
      : [['none', 'Sin regla'], ['enforced', 'Obligatoria'], ['default_once', 'Valor inicial']];
    options.forEach(function (option) {
      const item = document.createElement('option'); item.value = option[0]; item.textContent = option[1]; control.appendChild(item);
    });
    control.value = local ? local.policy : (inherited ? 'inherit' : 'none');
    if (inherited && !canOverride) {
      control.disabled = true;
      control.title = 'La regla del perfil base no permite cambiarse en esta rama.';
    }
    control.addEventListener('change', function () {
      if (control.value === 'inherit' || control.value === 'none') state.localConfigSettings.delete(id);
      else state.localConfigSettings.set(id, { path: path, format: entry.format, key: entry.key, value: entry.value, policy: control.value });
      invalidatePrepared();
    });
    row.append(identity, control);
    if (inherited) row.appendChild(make('span', 'config-rule-origin', canOverride ? (local ? 'Sustituye regla heredada' : 'Regla heredada') : 'Fijada por el perfil base'));
    else row.appendChild(make('span', 'config-rule-origin', local ? 'Regla de esta rama' : ''));
    fragment.appendChild(row);
  });
  rows.replaceChildren(fragment);
  qs('#toml-rule-empty').hidden = entries.length > 0;
  qs('#toml-rule-help').textContent = format === 'text_lines'
    ? 'Cada línea se identifica por su número. Si insertas o quitas líneas antes de una regla, cambiará su identidad.'
    : format === 'properties'
      ? 'Las propiedades se reconocen por su clave. Se conservan comentarios y formato; las claves escapadas o duplicadas usan la última aparición reconocida.'
      : 'Edita el TOML a la izquierda. En cada ajuste puedes conservar la regla heredada, imponer el valor o usarlo como valor inicial. Las demás líneas y comentarios se conservan.';
}

function syncConfigRulesFromEditor() {
  const path = state.editorPath;
  const kind = (state.parentMap.get(path) || {}).kind || (state.selectedFiles.get(path) || {}).kind;
  const entries = new Map(parseConfigRuleEntries(path, qs('#file-editor-content').value, kind).map(function (entry) { return [entry.key, entry]; }));
  Array.from(state.localConfigSettings.entries()).forEach(function (pair) {
    const id = pair[0], rule = pair[1];
    if (rule.path !== path) return;
    const current = entries.get(rule.key);
    if (!current) state.localConfigSettings.delete(id);
    else rule.value = current.value;
  });
}

function renderDiff() {
  const counts = { added: 0, changed: 0, removed: 0, unsupported: 0 };
  let stagingBytes = 0;
  state.diff.forEach(function (entry) {
    counts[entry.status] = (counts[entry.status] || 0) + 1;
    if ((entry.status === 'added' || entry.status === 'changed') && entry.local) stagingBytes += entry.local.size;
  });

  qs('#diff-added').textContent = String(counts.added);
  qs('#diff-changed').textContent = String(counts.changed);
  qs('#diff-removed').textContent = String(counts.removed);
  qs('#diff-bytes').textContent = formatBytes(stagingBytes);
  const hasBase = Boolean(state.parentRef);
  qs('#diff-changed-card').hidden = !hasBase;
  qs('#diff-removed-card').hidden = !hasBase;
  qs('#diff-stats').classList.toggle('independent', !hasBase);
  nodes.diffContext.textContent = hasBase
    ? 'Las diferencias afectan solo al perfil derivado: los cambios reemplazan archivos heredados y las eliminaciones los excluyen. El perfil base permanece intacto.'
    : 'Perfil independiente: todos los archivos elegidos serán altas. No se cambiarán ni eliminarán archivos de otro perfil.';

  if (counts.unsupported) {
    nodes.diffWarning.hidden = false;
    nodes.diffWarning.textContent = counts.unsupported + ' archivo(s) no se pueden publicar con este formato. Corrige la lista antes de continuar.';
  } else {
    nodes.diffWarning.hidden = true;
    nodes.diffWarning.textContent = '';
  }

  if (!state.diff.length) {
    const row = document.createElement('tr');
    const emptyMessage = !state.folderSelected
      ? 'Selecciona una carpeta para ver los cambios.'
      : (state.selectedFiles.size ? 'No hay cambios respecto al perfil base.' : 'La carpeta seleccionada está vacía; se excluirán los archivos heredados que permita quitar.');
    const cell = make('td', 'muted-cell', emptyMessage);
    cell.colSpan = 5;
    row.appendChild(cell);
    nodes.diffBody.replaceChildren(row);
    renderExplorer();
    return;
  }

  const fragment = document.createDocumentFragment();
  state.diff.forEach(function (entry) {
    const row = document.createElement('tr');

    const changeCell = document.createElement('td');
    const badge = make('span', 'change-badge change-' + entry.status, changeLabel(entry.status));
    changeCell.appendChild(badge);

    const pathCell = document.createElement('td');
    const code = make('code', '', entry.path);
    pathCell.appendChild(code);
    if (entry.reason) pathCell.appendChild(make('p', 'card-meta', entry.reason));

    const kindCell = make('td', '', kindLabel(entry.kind));

    const policyCell = document.createElement('td');
    if (entry.kind === 'config' && entry.status !== 'unsupported') {
      if (entry.status === 'removed') {
        policyCell.textContent = entry.parent && entry.parent.policy ? entry.parent.policy : '—';
      } else {
        const select = make('select', 'policy-select');
        select.setAttribute('aria-label', 'Regla de ' + entry.path);
        [['enforced', 'Obligatoria'], ['default_once', 'Valor inicial']].forEach(function (policy) {
          const option = document.createElement('option');
          option.value = policy[0];
          option.textContent = policy[1];
          select.appendChild(option);
        });
        select.value = policyFor(entry);
        select.addEventListener('change', function () {
          state.policies.set(entry.path, select.value);
          invalidatePrepared();
        });
        policyCell.appendChild(select);
      }
    } else if (entry.kind === 'mod' && entry.status !== 'removed' && entry.status !== 'unsupported') {
      const input = make('input', 'policy-select');
      input.value = state.modIds.get(entry.path) || defaultModId(entry.path);
      input.setAttribute('aria-label', 'ID del mod ' + entry.path);
      input.addEventListener('change', function () {
        state.modIds.set(entry.path, input.value.trim());
        invalidatePrepared();
      });
      policyCell.appendChild(input);
    } else {
      policyCell.textContent = '—';
    }

    const sizeCell = make('td', '', entry.local ? formatBytes(entry.local.size) : '—');
    row.append(changeCell, pathCell, kindCell, policyCell, sizeCell);
    fragment.appendChild(row);
  });

  nodes.diffBody.replaceChildren(fragment);
  renderExplorer();
}

function objectRef(local) {
  const ref = {
    sha256: local.sha256,
    size: local.size
  };
  if (local.mediaType) ref.media_type = local.mediaType;
  return ref;
}

function requiredValue(selector, label) {
  const value = qs(selector).value.trim();
  if (!value) throw new Error('Falta ' + label + '.');
  return value;
}

function buildManifest() {
  const unsupported = state.diff.filter(function (entry) { return entry.status === 'unsupported'; });
  if (unsupported.length) throw new Error('El diff contiene entradas no compatibles que deben corregirse antes de publicar.');
  if (!state.selectedFiles.size && !state.diff.some(function (entry) { return entry.status === 'removed'; }) && !state.parentRef) {
    throw new Error('Selecciona un pack antes de preparar la publicación.');
  }

  const profile = requiredValue('#profile-id', 'el ID del perfil');
  const name = requiredValue('#profile-name', 'el nombre del perfil');
  const revision = requiredValue('#revision-id', 'el ID de revisión');
  const minecraft = requiredValue('#minecraft-version', 'la versión de Minecraft');
  const neoforge = requiredValue('#neoforge-version', 'la versión de NeoForge');
  if (!state.minecraftVersions.includes(minecraft)) throw new Error('Selecciona una versión de Minecraft del catálogo.');
  if (!allowedNeoForgeVersions().includes(neoforge)) throw new Error('Selecciona una versión de NeoForge compatible con Minecraft ' + minecraft + '.');
  const sequence = Number(qs('#revision-sequence').value);
  if (!Number.isSafeInteger(sequence) || sequence < 1) throw new Error('La secuencia debe ser un entero positivo.');

  if (nodes.parentProfile.value && !state.parentRef) {
    throw new Error('La revisión madre seleccionada no está resuelta.');
  }
  if (state.parentRef && state.parentDepth >= MAX_INHERITANCE_LEVELS) {
    throw new Error('No se puede crear este perfil: la cadena superaría el máximo de 8 niveles heredados.');
  }
  for (const rule of state.localConfigSettings.values()) {
    if (!state.selectedFiles.has(rule.path) && (!state.parentMap.has(rule.path) || state.removedPaths.has(rule.path))) {
      throw new Error('El ajuste ' + rule.key + ' apunta a un archivo de configuración que ya no forma parte de esta rama.');
    }
  }

  const manifest = {
    schema_version: state.localConfigSettings.size ? 2 : 1,
    revision: {
      id: revision,
      sequence: sequence,
      created_at: new Date().toISOString()
    },
    profile: {
      id: profile,
      name: name,
      official: true
    },
    game: {
      minecraft: minecraft,
      neoforge: neoforge
    },
    base: state.parentRef ? {
      profile_id: state.parentRef.profile_id,
      revision_id: state.parentRef.revision_id,
      manifest_sha256: state.parentRef.manifest_sha256
    } : null,
    permissions: {
      derive_local: qs('#derive-local').checked,
      mods: {
        add: qs('#mods-add').checked,
        remove: qs('#mods-remove').checked
      },
      configs: {
        override_enforced: qs('#config-enforced').checked,
        override_default_once: qs('#config-default').checked
      },
      // Kept at the protocol's fixed cap for schema-v1 compatibility. The
      // server ignores this legacy per-profile value when resolving chains.
      max_inheritance_depth: 8
    },
    mods: [],
    remove_mods: [],
    configs: [],
    remove_configs: [],
    config_settings: Array.from(state.localConfigSettings.values()),
    objects: []
  };

  const notes = qs('#release-notes').value.trim();
  if (notes) manifest.profile.description = notes;
  if (state.icon) manifest.profile.icon = { sha256: state.icon.sha256, size: state.icon.file.size, media_type: "image/png" };

  state.diff.forEach(function (entry) {
    if (entry.status === 'added' || entry.status === 'changed') {
      const local = entry.local;
      const expected = entry.parent ? objectDigest(entry.parent) : '';
      if (entry.kind === 'mod') {
        const modId = String(state.modIds.get(entry.path) || defaultModId(entry.path)).trim();
        if (!modId) throw new Error('Falta el ID del mod ' + entry.path + '.');
        const mod = {
          id: modId,
          path: entry.path,
          object: objectRef(local)
        };
        if (expected) mod.expect_base_object_sha256 = expected;
        manifest.mods.push(mod);
      } else if (entry.kind === 'config') {
        const config = {
          path: entry.path,
          object: objectRef(local),
          policy: policyFor(entry)
        };
        if (expected) config.expect_base_object_sha256 = expected;
        manifest.configs.push(config);
      } else {
        const object = {
          id: entry.parent && entry.parent.id ? entry.parent.id : defaultObjectId(entry.path),
          path: entry.path,
          object: objectRef(local)
        };
        if (expected) object.expect_base_object_sha256 = expected;
        manifest.objects.push(object);
      }
    } else if (entry.status === 'removed') {
      const expected = objectDigest(entry.parent);
      if (!expected) throw new Error('La eliminación de ' + entry.path + ' no tiene SHA-256 madre verificable.');
      if (entry.kind === 'mod') {
        const id = valueText(entry.parent && entry.parent.id, '');
        if (!id) throw new Error('La eliminación de mod ' + entry.path + ' no tiene ID madre.');
        manifest.remove_mods.push({
          id: id,
          expect_base_object_sha256: expected
        });
      } else if (entry.kind === 'config') {
        manifest.remove_configs.push({
          path: entry.path,
          expect_base_object_sha256: expected
        });
      }
    }
  });

  return manifest;
}

function canonicalize(value) {
  if (value === null) return 'null';
  if (typeof value === 'string') return JSON.stringify(value);
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new Error('El manifest contiene un número no finito.');
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return '[' + value.map(canonicalize).join(',') + ']';
  }
  if (typeof value === 'object') {
    const keys = Object.keys(value).filter(function (key) { return value[key] !== undefined; }).sort();
    return '{' + keys.map(function (key) {
      return JSON.stringify(key) + ':' + canonicalize(value[key]);
    }).join(',') + '}';
  }
  throw new Error('El manifest contiene un valor no serializable.');
}

async function uploadObject(entry) {
  const path = '/v1/admin/objects/sha256/' + encodeURIComponent(entry.local.sha256);
  const headers = {
    'Content-Type': entry.local.mediaType || 'application/octet-stream'
  };

  await request(path, {
    method: 'POST',
    headers: headers,
    body: entry.local.file
  });
}

async function prepareAndStage() {
  invalidatePrepared();
    setStatus(nodes.stageStatus, 'Preparando archivos…');
  nodes.stageButton.disabled = true;

  try {
    const manifest = buildManifest();
    const canonical = canonicalize(manifest);
    const digest = await sha256Text(canonical);
    const uploads = state.diff.filter(function (entry) {
      return (entry.status === 'added' || entry.status === 'changed') && entry.local;
    });

    if (state.icon) uploads.push({ local: { ...state.icon, mediaType: "image/png" } });

    state.manifest = manifest;
    state.canonicalManifest = canonical;
    state.manifestSHA256 = digest;

    nodes.stageProgress.hidden = false;
    nodes.stageProgress.max = Math.max(uploads.length, 1);
    nodes.stageProgress.value = 0;

    for (let index = 0; index < uploads.length; index += 1) {
      setStatus(nodes.stageStatus, 'Subiendo archivo ' + (index + 1) + ' de ' + uploads.length + '…');
      await uploadObject(uploads[index]);
      nodes.stageProgress.value = index + 1;
    }

    if (!uploads.length) nodes.stageProgress.value = 1;
    state.staged = true;
    updatePublishEnabled();
    setStatus(nodes.stageStatus, 'Archivos preparados. Revisa los cambios y publica la versión.', 'success');
  } catch (error) {
    state.staged = false;
    setStatus(nodes.stageStatus, 'No se pudieron preparar los archivos: ' + describeError(error), 'error');
  } finally {
    nodes.stageButton.disabled = false;
  }
}

function signingRequestText() {
  if (!state.staged || !state.manifest || !state.manifestSHA256) {
    throw new Error('Primero prepara y sube los archivos.');
  }

  const requestPayload = {
    canonicalization: 'RFC8785-JCS',
    manifest_sha256: state.manifestSHA256,
    manifest: state.manifest
  };
  const canonicalRequest = canonicalize(requestPayload);
  return canonicalRequest + '\n';
}

function summarizeDiff() {
  const counts = { added: 0, changed: 0, removed: 0 };
  state.diff.forEach(function (entry) {
    if (counts[entry.status] !== undefined) counts[entry.status] += 1;
  });
  return [
    'Perfil: ' + valueText(state.manifest && state.manifest.profile && state.manifest.profile.id),
    'Versión: ' + valueText(state.manifest && state.manifest.revision && state.manifest.revision.id),
    'Altas: ' + counts.added,
    'Cambios: ' + counts.changed,
    'Eliminaciones: ' + counts.removed,
    'Perfil base: ' + (state.parentRef ? state.parentRef.profile_id + ' / ' + state.parentRef.revision_id : 'ninguno')
  ].join('\n');
}

function updatePublishEnabled() {
  nodes.publishButton.disabled = !(state.staged && state.manifest && nodes.confirmDiff.checked);
}

function confirmPublication() {
  if (!state.staged || !state.manifest || !nodes.confirmDiff.checked) return;
  nodes.dialogSummary.textContent = summarizeDiff();
  if (typeof nodes.dialog.showModal === 'function') {
    nodes.dialog.showModal();
  } else if (window.confirm('Confirmar publicación\n\n' + summarizeDiff())) {
    publishEnvelope();
  }
}

async function publishEnvelope() {
  if (!state.staged || !state.manifest) return;
  nodes.publishButton.disabled = true;
  setStatus(nodes.publishStatus, 'Publicando versión…');

  try {
    await request('/v1/admin/publications', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: signingRequestText()
    });
    const revision = state.manifest && state.manifest.revision ? state.manifest.revision.id : '';
    setStatus(nodes.publishStatus, 'Versión publicada' + (revision ? ': ' + revision : '') + '.', 'success');
    nodes.confirmDiff.checked = false;
    state.staged = false;
    qs('#revision-id').value = generatedRevisionID();
    updatePublishEnabled();
    await loadOverview();
    await loadProfiles();
  } catch (error) {
    setStatus(nodes.publishStatus, 'La publicación no fue aceptada: ' + describeError(error), 'error');
    updatePublishEnabled();
  }
}

function setupPickerSupport() {
  nodes.pickerSupport.hidden = true;
}

function setupCSRFHint() {
  const meta = document.querySelector('meta[name="csrf-token"]');
  if (meta && meta.content) state.csrfToken = meta.content;
}

function bindInvalidation() {
  const selectors = [
    '#profile-id',
    '#profile-name',
    '#revision-id',
    '#revision-sequence',
    '#minecraft-version',
    '#neoforge-version',
    '#release-notes',
    '#derive-local',
    '#mods-add',
    '#mods-remove',
    '#config-enforced',
    '#config-default'
  ];
  selectors.forEach(function (selector) {
    const node = qs(selector);
    node.addEventListener('input', invalidatePrepared);
    node.addEventListener('change', invalidatePrepared);
  });
  qs('#profile-name').addEventListener('input', function () {
    qs('#profile-id').value = generatedProfileID(qs('#profile-name').value);
    invalidatePrepared();
  });
}

function generatedProfileID(name) {
  const base = String(name || '').normalize('NFKD').replace(/[\u0300-\u036f]/g, '')
    .toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 44);
  if (!base) return '';
  const used = new Set(state.profiles.map(profileId));
  const candidate = 'profile_' + base;
  if (!used.has(candidate)) return candidate;
  let suffix = 2;
  while (used.has(candidate + '-' + suffix)) suffix += 1;
  return candidate + '-' + suffix;
}

function searchableCombobox(input, toggle, list, getOptions, onSelect) {
  let activeIndex = -1;
  let selectedValue = '';

  function close() {
    list.hidden = true;
    input.setAttribute('aria-expanded', 'false');
    toggle.setAttribute('aria-expanded', 'false');
    input.removeAttribute('aria-activedescendant');
    activeIndex = -1;
  }

  function render(query, open) {
    const normalized = String(query || '').trim().toLowerCase();
    const options = getOptions().filter(function (value) { return !normalized || value.toLowerCase().includes(normalized); });
    list.replaceChildren();
    activeIndex = -1;
    if (!options.length) {
      const empty = document.createElement('div');
      empty.className = 'combobox-empty';
      empty.textContent = normalized ? 'No hay coincidencias' : 'No hay versiones disponibles';
      list.appendChild(empty);
    } else {
      options.forEach(function (value, index) {
        const option = document.createElement('div');
        option.id = list.id + '-option-' + index;
        option.className = 'combobox-option';
        option.setAttribute('role', 'option');
        option.setAttribute('aria-selected', String(value === selectedValue));
        option.textContent = value;
        option.addEventListener('pointerdown', function (event) { event.preventDefault(); });
        option.addEventListener('click', function () {
          input.value = value;
          selectedValue = value;
          close();
          input.dispatchEvent(new Event('change', { bubbles: true }));
          onSelect(value);
        });
        list.appendChild(option);
      });
    }
    if (open) {
      list.hidden = false;
      input.setAttribute('aria-expanded', 'true');
      toggle.setAttribute('aria-expanded', 'true');
    }
  }

  function setActive(index) {
    const options = list.querySelectorAll('[role="option"]');
    if (!options.length) return;
    activeIndex = Math.max(0, Math.min(index, options.length - 1));
    options.forEach(function (option, optionIndex) {
      option.setAttribute('aria-selected', String(optionIndex === activeIndex));
    });
    input.setAttribute('aria-activedescendant', options[activeIndex].id);
    options[activeIndex].scrollIntoView({ block: 'nearest' });
  }

  input.addEventListener('focus', function () { render(input.value === selectedValue ? '' : input.value, true); });
  input.addEventListener('input', function () {
    if (input.value !== selectedValue) selectedValue = '';
    render(input.value, true);
    onSelect(input.value);
  });
  input.addEventListener('keydown', function (event) {
    const options = list.querySelectorAll('[role="option"]');
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      if (list.hidden) render('', true);
      setActive(activeIndex + 1);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      if (list.hidden) render('', true);
      setActive(activeIndex < 0 ? options.length - 1 : activeIndex - 1);
    } else if (event.key === 'Enter' && !list.hidden && activeIndex >= 0) {
      event.preventDefault();
      const option = list.querySelectorAll('[role="option"]')[activeIndex];
      if (option) option.click();
    } else if (event.key === 'Escape') {
      close();
    }
  });
  toggle.addEventListener('click', function () {
    if (list.hidden) {
      input.focus();
      render(input.value === selectedValue ? '' : input.value, true);
    } else close();
  });
  document.addEventListener('pointerdown', function (event) {
    if (!input.parentElement.contains(event.target)) close();
  });

  return {
    refresh: function () { render(input.value, !list.hidden); },
    close: close
  };
}

function neoforgeFamily(minecraftVersion) {
  if (!state.minecraftVersions.includes(minecraftVersion)) return '';
  const parts = minecraftVersion.split('.');
  if (parts[0] === '1') return (parts[1] || '') + '.' + (parts[2] || '0') + '.';
  return (parts[0] || '') + '.' + (parts[1] || '0') + '.';
}

function allowedNeoForgeVersions() {
  const prefix = neoforgeFamily(qs('#minecraft-version').value);
  return prefix ? state.neoForgeVersions.filter(function (version) { return version.startsWith(prefix); }) : [];
}

function updateNeoForgeOptions() {
  const neoForgeInput = qs('#neoforge-version');
  if (neoForgeInput.value && !allowedNeoForgeVersions().includes(neoForgeInput.value)) {
    neoForgeInput.value = '';
  }
  if (neoForgeCombobox) neoForgeCombobox.refresh();
  invalidatePrepared();
}

let minecraftCombobox;
let neoForgeCombobox;

async function loadGameVersions() {
  try {
    const catalog = await request('/admin/api/game-versions');
    state.minecraftVersions = Array.isArray(catalog.minecraft) ? catalog.minecraft : [];
    state.neoForgeVersions = Array.isArray(catalog.neoforge) ? catalog.neoforge : [];
    minecraftCombobox.refresh();
    neoForgeCombobox.refresh();
  } catch (error) {
    setStatus(nodes.parentStatus, 'No se pudo cargar el catálogo de versiones oficiales: ' + describeError(error), 'error');
  }
}

function bindEvents() {
  window.addEventListener('hashchange', route);
  nodes.parentProfile.addEventListener('change', handleParentProfileChange);
  nodes.parentRevision.addEventListener('change', handleParentRevisionChange);
  minecraftCombobox = searchableCombobox(
    qs('#minecraft-version'), qs('#minecraft-combobox .combobox-toggle'), qs('#minecraft-options'),
    function () { return state.minecraftVersions; }, updateNeoForgeOptions
  );
  neoForgeCombobox = searchableCombobox(
    qs('#neoforge-version'), qs('#neoforge-combobox .combobox-toggle'), qs('#neoforge-options'),
    allowedNeoForgeVersions, invalidatePrepared
  );
  qs('#minecraft-version').addEventListener('change', updateNeoForgeOptions);
  nodes.pickDirectory.addEventListener('click', chooseDirectory);
  nodes.addFiles.addEventListener('click', function () { state.pendingReplacePath = ''; state.pendingFolderPath = ''; nodes.filePicker.click(); });
  nodes.createFolder.addEventListener('click', function () { openCreateFolder(''); });
  nodes.filePicker.addEventListener('change', function () {
    addFiles(nodes.filePicker.files, state.pendingReplacePath, state.pendingFolderPath);
    nodes.filePicker.value = '';
  });
  nodes.folderFallback.addEventListener('change', function () {
    loadFallbackFiles(nodes.folderFallback.files);
    nodes.folderFallback.value = '';
  });
  nodes.folderFallback.addEventListener('cancel', function () {
    setStatus(nodes.folderStatus, 'Importación cancelada; se conserva la selección anterior.');
  });
  nodes.loadSynthetic.addEventListener('click', loadSyntheticPack);
  qs('#explorer-search').addEventListener('input', renderExplorer);
  qs('#file-editor-content').addEventListener('input', function () {
    if (state.editorPath && isConfigRuleFile(state.editorPath, (state.parentMap.get(state.editorPath) || {}).kind || (state.selectedFiles.get(state.editorPath) || {}).kind)) renderConfigRuleEditor();
  });
  const drop = qs('#explorer-drop');
  ['dragenter', 'dragover'].forEach(function (type) {
    drop.addEventListener(type, function (event) {
      event.preventDefault();
      drop.classList.add('is-dragover');
      drop.querySelectorAll('.is-folder-drop-target').forEach(function (node) { node.classList.remove('is-folder-drop-target'); });
      const target = event.target && event.target.closest('.tree-branch');
      if (target) target.classList.add('is-folder-drop-target');
    });
  });
  ['dragleave', 'drop'].forEach(function (type) {
    drop.addEventListener(type, function (event) {
      event.preventDefault();
      if (type === 'drop' || !drop.contains(event.relatedTarget)) {
        drop.classList.remove('is-dragover');
        drop.querySelectorAll('.is-folder-drop-target').forEach(function (node) { node.classList.remove('is-folder-drop-target'); });
      }
    });
  });
  drop.addEventListener('drop', function (event) {
    handleExplorerDrop(event);
  });
  qs('#file-editor-form').addEventListener('submit', function (event) {
    if (event.submitter && event.submitter.id === 'file-editor-save') {
      event.preventDefault();
      saveEditedFile();
    }
  });
  qs('#create-folder-form').addEventListener('submit', function (event) {
    if (event.submitter && event.submitter.id === 'create-folder-save') {
      event.preventDefault();
      createFolderFromDialog();
    }
  });
  nodes.stageButton.addEventListener('click', prepareAndStage);
  nodes.publishButton.addEventListener('click', confirmPublication);
  nodes.confirmDiff.addEventListener('change', updatePublishEnabled);
  qs('#check-service-update').addEventListener('click', checkServiceUpdate);
  qs('#apply-service-update').addEventListener('click', applyServiceUpdate);
  nodes.dialog.addEventListener('close', function () {
    if (nodes.dialog.returnValue === 'publish') publishEnvelope();
  });
  bindInvalidation();
}

async function init() {
  qs('#revision-id').value = generatedRevisionID();
  setupCSRFHint();
  setupPickerSupport();
  bindEvents();
  route();
  renderService();
  try {
    const session = await request('/admin/api/session');
    state.csrfToken = session && typeof session.csrf_token === 'string' ? session.csrf_token : '';
  } catch (error) {
    recordError('Sesión de administración', error);
    return;
  }
  await loadOverview();
  await loadProfiles();
  renderExplorer();
  await loadGameVersions();
}

function generatedRevisionID() {
  if (!window.crypto || typeof window.crypto.getRandomValues !== 'function') {
    throw new Error('No se puede generar un identificador seguro para la revisión.');
  }
  const bytes = new Uint8Array(16);
  window.crypto.getRandomValues(bytes);
  const suffix = Array.from(bytes, function (byte) { return byte.toString(16).padStart(2, '0'); }).join('');
  return 'rev_' + suffix;
}

init();

// Profile artwork is signed metadata, independent of the game's file tree.
async function normalizedProfileIcon(file) {
  if (file.size > 10 * 1024 * 1024 || !['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) throw new Error('Elige una imagen PNG, JPEG o WebP de hasta 10 MB.');
  const bitmap = await createImageBitmap(file);
  try {
    const canvas = document.createElement('canvas'); canvas.width = 256; canvas.height = 256;
    const context = canvas.getContext('2d');
    const scale = Math.min(256 / bitmap.width, 256 / bitmap.height);
    const w = bitmap.width * scale, h = bitmap.height * scale;
    context.drawImage(bitmap, (256-w)/2, (256-h)/2, w, h);
    const blob = await new Promise(resolve => canvas.toBlob(resolve, 'image/png'));
    if (!blob) throw new Error('No se pudo convertir la imagen.');
    const image = new File([blob], 'profile-icon.png', {type:'image/png'});
    return { file: image, sha256: await sha256Bytes(await image.arrayBuffer()) };
  } finally { bitmap.close(); }
}
async function selectProfileIcon(file) {
    if (!file) return;
    state.icon = await normalizedProfileIcon(file);
    const image = state.icon.file;
    if (state.iconURL) URL.revokeObjectURL(state.iconURL);
    state.iconURL = URL.createObjectURL(image);
    qs('#profile-icon-preview').src = state.iconURL; qs('#profile-icon-preview').hidden = false;
    qs('#remove-profile-icon').hidden = false;
    qs('#profile-icon-status').textContent = 'Icono seleccionado'; invalidatePrepared();
}
qs('#pick-profile-icon').addEventListener('click', () => qs('#profile-icon-file').click());
qs('#profile-icon-file').addEventListener('change', async event => {
  try { await selectProfileIcon(event.target.files[0]); } catch(error) { qs('#profile-icon-status').textContent = describeError(error); }
  event.target.value = '';
});
qs('#remove-profile-icon').addEventListener('click', () => {
  state.icon = null; if (state.iconURL) URL.revokeObjectURL(state.iconURL); state.iconURL = null;
  qs('#profile-icon-preview').hidden = true; qs('#remove-profile-icon').hidden = true;
  qs('#profile-icon-status').textContent = 'Sin icono'; invalidatePrepared();
});

async function editProfilePresentation(profile) {
  const dialog = make('dialog', 'profile-presentation-dialog');
  const content = make('div', 'panel');
  const current = profile.presentation || {};
  const name = make('input'); name.value = current.name || profileName(profile); name.maxLength = 96;
  const description = make('textarea'); description.value = current.description ?? profile.description ?? ''; description.maxLength = 8192; description.rows = 4;
  const picker = make('input'); picker.type = 'file'; picker.accept = 'image/png,image/jpeg,image/webp';
  const preview = make('img'); preview.width = preview.height = 64; preview.hidden = true;
  const remove = make('button', 'button button-secondary', 'Quitar icono'); remove.type='button';
  const save = make('button', 'button button-primary', 'Guardar cambios'); save.type='button';
  const close = make('button', 'button button-secondary', 'Cancelar'); close.type='button';
  const status = make('p', 'inline-status'); status.setAttribute('role','status');
  function field(text,input) { const label=make('label'); label.append(make('span','',text),input); return label; }
  content.append(make('h3','','Editar perfil'),field('Nombre',name),field('Descripción',description),field('Icono',picker),preview,remove,status,save,close);
  dialog.append(content); document.body.append(dialog); dialog.showModal();
  let icon=null, iconURL=null, currentIcon=current.icon ?? profile.icon ?? null;
  if(currentIcon?.sha256) { preview.src='/v1/objects/sha256/'+encodeURIComponent(currentIcon.sha256); preview.hidden=false; }
  picker.addEventListener('change',async()=>{
    if(!picker.files.length) return;
    save.disabled=true;
    try { icon=await normalizedProfileIcon(picker.files[0]); if(iconURL) URL.revokeObjectURL(iconURL); iconURL=URL.createObjectURL(icon.file); preview.src=iconURL; preview.hidden=false; status.textContent=''; }
    catch(error) { status.textContent=describeError(error); }
    finally { save.disabled=false; }
  });
  remove.addEventListener('click',()=>{icon=null;currentIcon=null;picker.value='';preview.hidden=true;status.textContent='Sin icono';});
  close.addEventListener('click',()=>dialog.close());
  save.addEventListener('click',async()=>{
    if(!name.value.trim()) { status.textContent='Escribe un nombre para el perfil.';name.focus();return; }
    save.disabled=close.disabled=true;status.textContent='Guardando…';
    try {
      if(icon) { await uploadObject({local:{...icon,mediaType:'image/png'}}); currentIcon={sha256:icon.sha256,size:icon.file.size,media_type:'image/png'}; }
      await request('/v1/admin/profiles/'+encodeURIComponent(profileId(profile))+'/presentation',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:name.value.trim(),description:description.value.trim(),icon:currentIcon})});
      await loadProfiles();dialog.close();
    } catch(error) { status.textContent=describeError(error); }
    finally { save.disabled=close.disabled=false; }
  });
  dialog.addEventListener('close',()=>{if(iconURL) URL.revokeObjectURL(iconURL);dialog.remove();});
}


function deleteGlobalProfile(profile) {
  const id = profileId(profile);
  const dialog = make('dialog', 'profile-presentation-dialog');
  const form = make('form', 'panel');
  const title = make('h3', '', 'Borrar perfil global');
  title.id = 'delete-profile-title';
  dialog.setAttribute('aria-labelledby', title.id);
  const label = make('label');
  const input = make('input');
  input.type = 'text'; input.autocomplete = 'off'; input.spellcheck = false;
  label.append(make('span', '', 'Escribe el identificador para confirmar'), make('code', '', id), input);
  const status = make('p', 'inline-status'); status.setAttribute('role', 'status');
  const cancel = make('button', 'button button-secondary', 'Cancelar'); cancel.type = 'button';
  const remove = make('button', 'button button-danger', 'Borrar perfil'); remove.type = 'submit'; remove.disabled = true;
  const actions = make('div', 'dialog-actions'); actions.append(cancel, remove);
  form.append(title, make('p', '', profileName(profile)), make('p', 'microcopy', 'El perfil dejará de aparecer en el catálogo. Las instancias ya instaladas y los perfiles derivados se conservarán. Su identificador no podrá reutilizarse.'), label, status, actions);
  dialog.append(form); document.body.append(dialog);
  let busy = false;
  input.addEventListener('input', () => { remove.disabled = busy || input.value !== id; });
  cancel.addEventListener('click', () => dialog.close());
  dialog.addEventListener('cancel', event => { if (busy) event.preventDefault(); });
  dialog.addEventListener('close', () => dialog.remove());
  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (busy || input.value !== id) return;
    busy = true; remove.disabled = cancel.disabled = input.disabled = true;
    status.textContent = 'Borrando perfil…';
    try {
      await request('/v1/admin/profiles/' + encodeURIComponent(id), {method:'DELETE',headers:{'Content-Type':'application/json'},body:JSON.stringify({confirm_profile_id:input.value})});
      const resetBase = nodes.parentProfile.value === id;
      await loadProfiles(); await loadOverview();
      if (resetBase) { nodes.parentProfile.value = ''; await handleParentProfileChange(); }
      dialog.close();
    } catch (error) { status.textContent = 'No se pudo borrar el perfil: ' + describeError(error); }
    finally { busy = false; cancel.disabled = input.disabled = false; remove.disabled = input.value !== id; }
  });
  dialog.showModal(); input.focus();
}
