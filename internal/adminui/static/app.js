'use strict';

const state = {
  overview: null,
  build: {},
  profiles: [],
  recentRevisions: [],
  parentMap: new Map(),
  parentRef: null,
  parentManifest: null,
  selectedFiles: new Map(),
  pathProblems: [],
  diff: [],
  policies: new Map(),
  modIds: new Map(),
  manifest: null,
  canonicalManifest: '',
  manifestSHA256: '',
  signedEnvelope: null,
  staged: false,
  csrfToken: '',
  overviewSource: '',
  apiErrors: []
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
  folderFallback: document.querySelector('#folder-fallback'),
  loadSynthetic: document.querySelector('#load-synthetic'),
  folderStatus: document.querySelector('#folder-status'),
  pickerSupport: document.querySelector('#picker-support'),
  syntheticHint: document.querySelector('#synthetic-hint'),
  diffBody: document.querySelector('#diff-body'),
  diffWarning: document.querySelector('#diff-warning'),
  stageButton: document.querySelector('#stage-button'),
  stageStatus: document.querySelector('#stage-status'),
  stageProgress: document.querySelector('#stage-progress'),
  downloadRequest: document.querySelector('#download-request'),
  signedEnvelope: document.querySelector('#signed-envelope'),
  envelopeStatus: document.querySelector('#envelope-status'),
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
    const code = error.code ? ' · ' + error.code : '';
    return error.message + code;
  }
  return error instanceof Error ? error.message : String(error);
}

function recordError(context, error, globalAlert) {
  const message = context + ': ' + describeError(error);
  state.apiErrors.push(message);
  if (globalAlert !== false) addAlert(message);
  nodes.apiState.textContent = 'API con errores';
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
    const code = payload && typeof payload === 'object'
      ? (payload.code || payload.error_code || payload.error || '')
      : '';
    const serverMessage = payload && typeof payload === 'object'
      ? (payload.message || payload.detail || payload.error_description || '')
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
  const storage = state.overview && state.overview.storage && typeof state.overview.storage === 'object'
    ? state.overview.storage
    : {};
  const revisionCount = storage.revision_count !== undefined
    ? storage.revision_count
    : state.recentRevisions.length;

  qs('#metric-profiles').textContent = String(state.profiles.length);
  qs('#metric-revisions').textContent = String(Number.isFinite(Number(revisionCount)) ? Number(revisionCount) : state.recentRevisions.length);
  qs('#metric-activity').textContent = String(state.recentRevisions.length);
  qs('#metric-profiles-note').textContent = state.profiles.length === 1 ? '1 perfil conocido' : state.profiles.length + ' perfiles conocidos';

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
        return valueText(channel && (channel.name || channel.channel), '');
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
    nodes.activity.textContent = 'Sin revisiones recientes.';
    return;
  }

  const list = make('div', 'activity-list');
  state.recentRevisions.slice(0, 8).forEach(function (revision) {
    const item = make('article', 'activity-item');
    const name = valueText(revision.profile_name || revision.profile_id, 'Perfil');
    const sequence = revisionSequence(revision);
    const title = make('strong', '', name + (sequence !== null ? ' · secuencia ' + sequence : ''));
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
        renderProfileRevisions(revisionContainer, revisions);
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
  const payload = await request('/v1/admin/ui/profiles/' + encodeURIComponent(id) + '/revisions');
  return listFrom(payload, ['revisions', 'items']);
}

function renderProfileRevisions(container, revisions) {
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
      make('p', 'card-meta', seq === null ? 'Secuencia no disponible' : 'Secuencia ' + seq)
    );
    const digest = revisionDigest(revision);
    if (digest) card.appendChild(make('p', 'card-meta', 'Manifest SHA-256: ' + digest));
    const when = revision.published_at || revision.created_at;
    if (when) card.appendChild(make('p', 'card-meta', valueText(when)));
    fragment.appendChild(card);
  });
  container.replaceChildren(fragment);
}

function populateParentProfiles() {
  const current = nodes.parentProfile.value;
  const fragment = document.createDocumentFragment();
  const root = document.createElement('option');
  root.value = '';
  root.textContent = 'Sin revisión madre';
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
  qs('#service-picker').textContent = typeof window.showDirectoryPicker === 'function' ? 'Disponible' : 'No disponible; se usa la alternativa';
  qs('#service-secure').textContent = window.isSecureContext ? 'Sí' : 'No';
  qs('#service-crypto').textContent = window.crypto && window.crypto.subtle ? 'Disponible' : 'No disponible';
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
  return name.replace(/\.jar$/i, '').replace(/[^A-Za-z0-9._-]+/g, '-');
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

async function collectDirectory(handle, prefix, out) {
  for await (const entryPair of handle.entries()) {
    const name = entryPair[0];
    const child = entryPair[1];
    const path = prefix ? prefix + '/' + name : name;
    if (child.kind === 'file') {
      out.push({ path: path, file: await child.getFile() });
    } else if (child.kind === 'directory') {
      await collectDirectory(child, path, out);
    }
  }
}

function fallbackPath(file) {
  const relative = typeof file.webkitRelativePath === 'string' ? file.webkitRelativePath : '';
  if (!relative) return file.name;
  const parts = relative.split('/');
  return parts.length > 1 ? parts.slice(1).join('/') : file.name;
}

async function scanRecords(records, label) {
  const previousFiles = state.selectedFiles;
  const previousProblems = state.pathProblems;
  const nextFiles = new Map();
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
      nextFiles.set(path, {
        path: path,
        file: file,
        sha256: digest,
        size: Number(file.size) || 0,
        mediaType: typeof file.type === 'string' ? file.type : '',
        kind: classifyPath(path)
      });
      if (classifyPath(path) === 'mod' && !state.modIds.has(path)) {
        state.modIds.set(path, defaultModId(path));
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
  invalidatePrepared();
  await recomputeDiff();
  const total = Array.from(nextFiles.values()).reduce(function (sum, entry) { return sum + entry.size; }, 0);
  setStatus(
    nodes.folderStatus,
    label + ': ' + nextFiles.size + ' archivos · ' + formatBytes(total) + (problems.length ? ' · ' + problems.length + ' rutas no compatibles' : ''),
    problems.length ? 'warning' : 'success'
  );
}

async function chooseDirectory() {
  if (typeof window.showDirectoryPicker !== 'function') {
    nodes.folderFallback.click();
    return;
  }

  const previousMessage = nodes.folderStatus.textContent;
  try {
    const handle = await window.showDirectoryPicker({ mode: 'read' });
    const records = [];
    await collectDirectory(handle, '', records);
    await scanRecords(records, 'Carpeta ' + valueText(handle.name, 'seleccionada'));
  } catch (error) {
    if (error && error.name === 'AbortError') {
      setStatus(nodes.folderStatus, previousMessage || 'Selección cancelada; no se han perdido los archivos anteriores.');
      return;
    }
    setStatus(nodes.folderStatus, 'No se pudo abrir la carpeta. Se conservan los archivos anteriores. ' + describeError(error), 'error');
  }
}

function loadFallbackFiles(fileList) {
  const records = Array.from(fileList || []).map(function (file) {
    return { path: fallbackPath(file), file: file };
  });
  if (!records.length) return;
  scanRecords(records, 'Selección alternativa');
}

function syntheticRecords(variant) {
  if (variant === 'child') {
    return [
      { path: 'mods/bootoptim-synthetic.jar', file: new File(['synthetic-mod-v2'], 'bootoptim-synthetic.jar', { type: 'application/java-archive' }) },
      { path: 'config/bootoptim-synthetic.toml', file: new File(['enabled=true\nlevel=2\n'], 'bootoptim-synthetic.toml', { type: 'text/plain' }) },
      { path: 'resourcepacks/bootoptim-synthetic/pack.mcmeta', file: new File(['{"pack":{"description":"synthetic v2","pack_format":1}}'], 'pack.mcmeta', { type: 'application/json' }) },
      { path: 'resourcepacks/bootoptim-synthetic/credits.txt', file: new File(['synthetic child fixture\n'], 'credits.txt', { type: 'text/plain' }) }
    ];
  }

  return [
    { path: 'mods/bootoptim-synthetic.jar', file: new File(['synthetic-mod-v1'], 'bootoptim-synthetic.jar', { type: 'application/java-archive' }) },
    { path: 'mods/remove-me.jar', file: new File(['synthetic-removal-target-v1'], 'remove-me.jar', { type: 'application/java-archive' }) },
    { path: 'config/bootoptim-synthetic.toml', file: new File(['enabled=true\nlevel=1\n'], 'bootoptim-synthetic.toml', { type: 'text/plain' }) },
    { path: 'resourcepacks/bootoptim-synthetic/pack.mcmeta', file: new File(['{"pack":{"description":"synthetic root","pack_format":1}}'], 'pack.mcmeta', { type: 'application/json' }) }
  ];
}

async function loadSyntheticPack() {
  let variant = 'root';
  if (state.parentRef) {
    if (!state.parentMap.has('mods/bootoptim-synthetic.jar') || !state.parentMap.has('mods/remove-me.jar')) {
      setStatus(nodes.folderStatus, 'La revisión madre elegida no parece ser el fixture sintético raíz. Para evitar un diff engañoso, selecciona “Sin revisión madre” o una revisión sintética compatible.', 'warning');
      return;
    }
    variant = 'child';
  }
  await scanRecords(syntheticRecords(variant), variant === 'child' ? 'Pack sintético hijo' : 'Pack sintético raíz');
}

function objectDigest(entry) {
  return entry && entry.object && typeof entry.object.sha256 === 'string' ? entry.object.sha256 : '';
}

function applyManifest(map, manifest) {
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
    if (typeof removal.path === 'string') map.delete(removal.path);
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
  if (depth > 8) throw new Error('La herencia supera la profundidad máxima admitida por el contrato.');
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
  }

  applyManifest(map, manifest);
  seen.delete(key);

  const manifestProfile = manifest.profile && valueText(manifest.profile.id, profile);
  const manifestRevision = manifest.revision && valueText(manifest.revision.id, revision);
  return {
    map: map,
    manifest: manifest,
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
  state.parentRef = null;
  state.parentManifest = null;

  const id = nodes.parentProfile.value;
  nodes.parentRevision.disabled = !id;
  if (!id) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'Selecciona un perfil primero';
    nodes.parentRevision.replaceChildren(option);
    setStatus(nodes.parentStatus, 'Publicación raíz: no se comparará contra una revisión anterior.');
    nodes.syntheticHint.textContent = 'usa el pack sintético raíz para validar el flujo antes de seleccionar datos reales.';
    await recomputeDiff();
    return;
  }

  setStatus(nodes.parentStatus, 'Cargando revisiones de ' + id + '…');
  try {
    const revisions = await fetchProfileRevisions(id);
    const fragment = document.createDocumentFragment();
    const placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.textContent = 'Selecciona una revisión';
    fragment.appendChild(placeholder);
    revisions.forEach(function (revision) {
      const idValue = revisionId(revision);
      if (!idValue) return;
      const option = document.createElement('option');
      option.value = idValue;
      const seq = revisionSequence(revision);
      option.textContent = idValue + (seq === null ? '' : ' · secuencia ' + seq);
      fragment.appendChild(option);
    });
    nodes.parentRevision.replaceChildren(fragment);
    setStatus(nodes.parentStatus, revisions.length ? 'Selecciona la revisión madre exacta.' : 'Este perfil no expone revisiones utilizables.', revisions.length ? '' : 'warning');
  } catch (error) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'No se pudieron cargar revisiones';
    nodes.parentRevision.replaceChildren(option);
    nodes.parentRevision.disabled = true;
    setStatus(nodes.parentStatus, 'No se pudo cargar la revisión madre: ' + describeError(error), 'error');
  }
  await recomputeDiff();
}

async function handleParentRevisionChange() {
  invalidatePrepared();
  state.parentMap = new Map();
  state.parentRef = null;
  state.parentManifest = null;

  const profile = nodes.parentProfile.value;
  const revision = nodes.parentRevision.value;
  if (!profile || !revision) {
    setStatus(nodes.parentStatus, 'Selecciona una revisión madre exacta.', 'warning');
    await recomputeDiff();
    return;
  }

  setStatus(nodes.parentStatus, 'Resolviendo manifest efectivo y herencia fijada…');
  try {
    const resolved = await resolveEffective(profile, revision, 0, new Set());
    state.parentMap = resolved.map;
    state.parentRef = resolved.ref;
    state.parentManifest = resolved.manifest;
    if (Number.isFinite(resolved.sequence)) {
      qs('#revision-sequence').value = String(Math.max(1, resolved.sequence + 1));
    }
    setStatus(
      nodes.parentStatus,
      'Madre fijada: ' + resolved.ref.profile_id + ' / ' + resolved.ref.revision_id + ' · ' + resolved.ref.manifest_sha256,
      'success'
    );
    nodes.syntheticHint.textContent = state.parentMap.has('mods/bootoptim-synthetic.jar')
      ? 'la madre parece sintética; el botón cargará el fixture hijo con cambio, alta y eliminación.'
      : 'para el fixture hijo selecciona primero una revisión creada con el pack sintético raíz.';
  } catch (error) {
    setStatus(nodes.parentStatus, 'No se pudo resolver la revisión madre: ' + describeError(error), 'error');
  }
  await recomputeDiff();
}

function invalidatePrepared() {
  state.manifest = null;
  state.canonicalManifest = '';
  state.manifestSHA256 = '';
  state.signedEnvelope = null;
  state.staged = false;
  nodes.downloadRequest.disabled = true;
  nodes.publishButton.disabled = true;
  nodes.confirmDiff.checked = false;
  setStatus(nodes.envelopeStatus, 'Ningún envelope cargado.');
  setStatus(nodes.publishStatus, '');
}

async function recomputeDiff() {
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
    if (state.selectedFiles.has(path)) return;
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

  if (counts.unsupported) {
    nodes.diffWarning.hidden = false;
    nodes.diffWarning.textContent = counts.unsupported + ' entrada(s) no pueden representarse con seguridad en el manifest actual. La publicación queda bloqueada hasta corregirlas.';
  } else {
    nodes.diffWarning.hidden = true;
    nodes.diffWarning.textContent = '';
  }

  if (!state.diff.length) {
    const row = document.createElement('tr');
    const cell = make('td', 'muted-cell', state.selectedFiles.size ? 'No hay cambios respecto a la revisión madre.' : 'Selecciona una carpeta para calcular el diff.');
    cell.colSpan = 5;
    row.appendChild(cell);
    nodes.diffBody.replaceChildren(row);
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
        select.setAttribute('aria-label', 'Política de ' + entry.path);
        ['enforced', 'default_once', 'user_owned'].forEach(function (value) {
          const option = document.createElement('option');
          option.value = value;
          option.textContent = value;
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
  if (!state.selectedFiles.size && !state.diff.some(function (entry) { return entry.status === 'removed'; })) {
    throw new Error('Selecciona un pack antes de preparar la publicación.');
  }

  const profile = requiredValue('#profile-id', 'el ID del perfil');
  const name = requiredValue('#profile-name', 'el nombre del perfil');
  const revision = requiredValue('#revision-id', 'el ID de revisión');
  const minecraft = requiredValue('#minecraft-version', 'la versión de Minecraft');
  const neoforge = requiredValue('#neoforge-version', 'la versión de NeoForge');
  const sequence = Number(qs('#revision-sequence').value);
  if (!Number.isSafeInteger(sequence) || sequence < 1) throw new Error('La secuencia debe ser un entero positivo.');

  if (nodes.parentProfile.value && !state.parentRef) {
    throw new Error('La revisión madre seleccionada no está resuelta.');
  }

  const maxDepth = Number(qs('#max-depth').value);
  if (!Number.isSafeInteger(maxDepth) || maxDepth < 1 || maxDepth > 8) {
    throw new Error('La profundidad máxima de herencia debe estar entre 1 y 8.');
  }

  const manifest = {
    schema_version: 1,
    revision: {
      id: revision,
      sequence: sequence,
      created_at: new Date().toISOString()
    },
    profile: {
      id: profile,
      name: name,
      official: qs('#profile-official').checked
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
      max_inheritance_depth: maxDepth
    },
    mods: [],
    remove_mods: [],
    configs: [],
    remove_configs: [],
    objects: []
  };

  const notes = qs('#release-notes').value.trim();
  if (notes) manifest.revision.release_notes = notes;

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
          id: entry.parent && entry.parent.id ? entry.parent.id : entry.path,
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

  try {
    await request(path, {
      method: 'POST',
      headers: headers,
      body: entry.local.file
    });
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 405) throw error;
    await request(path, {
      method: 'PUT',
      headers: headers,
      body: entry.local.file
    });
  }
}

async function prepareAndStage() {
  invalidatePrepared();
  setStatus(nodes.stageStatus, 'Preparando manifest y staging…');
  nodes.stageButton.disabled = true;

  try {
    const manifest = buildManifest();
    const canonical = canonicalize(manifest);
    const digest = await sha256Text(canonical);
    const uploads = state.diff.filter(function (entry) {
      return (entry.status === 'added' || entry.status === 'changed') && entry.local;
    });

    state.manifest = manifest;
    state.canonicalManifest = canonical;
    state.manifestSHA256 = digest;

    nodes.stageProgress.hidden = false;
    nodes.stageProgress.max = Math.max(uploads.length, 1);
    nodes.stageProgress.value = 0;

    for (let index = 0; index < uploads.length; index += 1) {
      setStatus(nodes.stageStatus, 'Subiendo objeto ' + (index + 1) + ' de ' + uploads.length + '…');
      await uploadObject(uploads[index]);
      nodes.stageProgress.value = index + 1;
    }

    if (!uploads.length) nodes.stageProgress.value = 1;
    state.staged = true;
    nodes.downloadRequest.disabled = false;
    setStatus(nodes.stageStatus, 'Staging aceptado por el API. Manifest SHA-256: ' + digest, 'success');
  } catch (error) {
    state.staged = false;
    nodes.downloadRequest.disabled = true;
    setStatus(nodes.stageStatus, 'No se completó el staging: ' + describeError(error), 'error');
  } finally {
    nodes.stageButton.disabled = false;
  }
}

function downloadSigningRequest() {
  if (!state.staged || !state.manifest || !state.manifestSHA256) {
    setStatus(nodes.stageStatus, 'Primero debe completarse el staging sin errores.', 'error');
    return;
  }

  const requestPayload = {
    canonicalization: 'RFC8785-JCS',
    manifest_sha256: state.manifestSHA256,
    manifest: state.manifest
  };
  const canonicalRequest = canonicalize(requestPayload);
  const blob = new Blob([canonicalRequest + '\n'], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = safeFilename(state.manifest.profile.id) + '-' + safeFilename(state.manifest.revision.id) + '.signing-request.json';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
  setStatus(nodes.stageStatus, 'Solicitud canónica descargada. Fírmala con la herramienta local y carga el envelope resultante.', 'success');
}

async function readSignedEnvelope(file) {
  if (!file) return;
  let envelope;
  try {
    envelope = JSON.parse(await file.text());
  } catch (error) {
    state.signedEnvelope = null;
    setStatus(nodes.envelopeStatus, 'El archivo elegido no contiene JSON válido.', 'error');
    updatePublishEnabled();
    return;
  }

  try {
    if (!state.staged || !state.manifest || !state.manifestSHA256) {
      throw new Error('No hay un staging preparado contra el que validar este envelope.');
    }
    if (!envelope || typeof envelope !== 'object') throw new Error('El envelope no es un objeto JSON.');
    if (envelope.canonicalization !== 'RFC8785-JCS') throw new Error('La canonicalización no es RFC8785-JCS.');
    if (envelope.manifest_sha256 !== state.manifestSHA256) throw new Error('El SHA-256 del envelope no coincide con el diff preparado.');

    const manifest = typeof envelope.manifest === 'string' ? JSON.parse(envelope.manifest) : envelope.manifest;
    if (!manifest || canonicalize(manifest) !== state.canonicalManifest) {
      throw new Error('El manifest firmado no coincide exactamente con el manifest preparado.');
    }

    const signature = envelope.signature;
    if (!signature || typeof signature !== 'object') throw new Error('Falta la firma.');
    if (signature.algorithm !== 'Ed25519') throw new Error('El algoritmo de firma debe ser Ed25519.');
    if (typeof signature.key_id !== 'string' || !signature.key_id) throw new Error('Falta key_id en la firma.');
    if (typeof signature.value !== 'string' || !signature.value) throw new Error('Falta el valor de firma.');

    state.signedEnvelope = envelope;
    setStatus(nodes.envelopeStatus, 'Envelope coherente con el diff preparado. La firma final será verificada por el servicio.', 'success');
  } catch (error) {
    state.signedEnvelope = null;
    setStatus(nodes.envelopeStatus, 'Envelope rechazado por el panel: ' + describeError(error), 'error');
  }

  updatePublishEnabled();
}

function summarizeDiff() {
  const counts = { added: 0, changed: 0, removed: 0 };
  state.diff.forEach(function (entry) {
    if (counts[entry.status] !== undefined) counts[entry.status] += 1;
  });
  return [
    'Perfil: ' + valueText(state.manifest && state.manifest.profile && state.manifest.profile.id),
    'Revisión: ' + valueText(state.manifest && state.manifest.revision && state.manifest.revision.id),
    'Manifest: ' + valueText(state.manifestSHA256),
    'Altas: ' + counts.added,
    'Cambios: ' + counts.changed,
    'Eliminaciones: ' + counts.removed,
    'Madre: ' + (state.parentRef ? state.parentRef.profile_id + ' / ' + state.parentRef.revision_id : 'raíz')
  ].join('\n');
}

function updatePublishEnabled() {
  nodes.publishButton.disabled = !(state.signedEnvelope && nodes.confirmDiff.checked);
}

function confirmPublication() {
  if (!state.signedEnvelope || !nodes.confirmDiff.checked) return;
  nodes.dialogSummary.textContent = summarizeDiff();
  if (typeof nodes.dialog.showModal === 'function') {
    nodes.dialog.showModal();
  } else if (window.confirm('Confirmar publicación\n\n' + summarizeDiff())) {
    publishEnvelope();
  }
}

async function publishEnvelope() {
  if (!state.signedEnvelope) return;
  nodes.publishButton.disabled = true;
  setStatus(nodes.publishStatus, 'Publicando revisión firmada…');

  try {
    await request('/v1/admin/revisions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(state.signedEnvelope)
    });
    const revision = state.manifest && state.manifest.revision ? state.manifest.revision.id : '';
    setStatus(nodes.publishStatus, 'El API aceptó la publicación' + (revision ? ' de ' + revision : '') + '.', 'success');
    nodes.confirmDiff.checked = false;
    state.signedEnvelope = null;
    updatePublishEnabled();
    await loadOverview();
    await loadProfiles();
  } catch (error) {
    setStatus(nodes.publishStatus, 'La publicación no fue aceptada: ' + describeError(error), 'error');
    updatePublishEnabled();
  }
}

function setupPickerSupport() {
  if (typeof window.showDirectoryPicker === 'function') {
    nodes.pickerSupport.textContent = 'Selector moderno disponible. La alternativa permanece disponible por compatibilidad.';
  } else {
    nodes.pickerSupport.textContent = 'Este navegador no ofrece showDirectoryPicker(). Usa “Alternativa de selección”; los archivos elegidos se conservan en memoria en esta pestaña mientras preparas el diff.';
  }
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
    '#profile-official',
    '#derive-local',
    '#mods-add',
    '#mods-remove',
    '#config-enforced',
    '#config-default',
    '#max-depth'
  ];
  selectors.forEach(function (selector) {
    const node = qs(selector);
    node.addEventListener('input', invalidatePrepared);
    node.addEventListener('change', invalidatePrepared);
  });
}

function bindEvents() {
  window.addEventListener('hashchange', route);
  nodes.parentProfile.addEventListener('change', handleParentProfileChange);
  nodes.parentRevision.addEventListener('change', handleParentRevisionChange);
  nodes.pickDirectory.addEventListener('click', chooseDirectory);
  nodes.folderFallback.addEventListener('change', function () {
    loadFallbackFiles(nodes.folderFallback.files);
    nodes.folderFallback.value = '';
  });
  nodes.loadSynthetic.addEventListener('click', loadSyntheticPack);
  nodes.stageButton.addEventListener('click', prepareAndStage);
  nodes.downloadRequest.addEventListener('click', downloadSigningRequest);
  nodes.signedEnvelope.addEventListener('change', function () {
    readSignedEnvelope(nodes.signedEnvelope.files && nodes.signedEnvelope.files[0]);
    nodes.signedEnvelope.value = '';
  });
  nodes.confirmDiff.addEventListener('change', updatePublishEnabled);
  nodes.publishButton.addEventListener('click', confirmPublication);
  nodes.dialog.addEventListener('close', function () {
    if (nodes.dialog.returnValue === 'publish') publishEnvelope();
  });
  bindInvalidation();
}

async function init() {
  setupCSRFHint();
  setupPickerSupport();
  bindEvents();
  route();
  renderService();
  await loadOverview();
  await loadProfiles();
}

init();
