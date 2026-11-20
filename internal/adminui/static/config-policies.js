'use strict';

// Config file policies («Obligatoria» = enforced, «Valor inicial» = default_once)
// chosen per file or per folder in the file explorer (docs/ADMIN_UI.md,
// «Políticas de configs»).
//
// Launchers only read the signed per-file configs[].policy, and Pandora rejects
// unknown manifest keys, so folder rules never enter the manifest: the panel
// expands them into configs[].policy when it builds the revision, and keeps the
// rules themselves as unsigned admin metadata of the profile
// (GET/PUT /v1/admin/profiles/{id}/config-policies). A child profile and the
// next version read them back, so the rule stays visible, is inherited and
// reaches files added later.
//
// Rule paths: "config/…/" (folder, subfolders included) or "config/…" (file).
// The most specific rule wins: file rule, then the deepest folder rule; with
// no rule the file keeps its published policy (or default_once when new).

const CONFIG_POLICY_LABELS = { enforced: 'Obligatoria', default_once: 'Valor inicial' };

function isConfigPolicyPath(path) {
  return path === 'config' || path === 'config/' || String(path || '').startsWith('config/');
}

function folderRulePath(directoryPath) {
  return directoryPath.endsWith('/') ? directoryPath : directoryPath + '/';
}

// Merges rule layers from the root ancestor to the draft: a later layer
// replaces the same path. Each layer is [{path, policy}] tagged with origin.
function mergeConfigPolicyLayers(layers) {
  const merged = new Map();
  layers.forEach(function (layer) {
    (layer.rules || []).forEach(function (rule) {
      if (!rule || typeof rule.path !== 'string' || !CONFIG_POLICY_LABELS[rule.policy]) return;
      merged.set(rule.path, { policy: rule.policy, origin: layer.origin || '' });
    });
  });
  return merged;
}

// Returns {policy, source, rulePath} for a config file. rules: Map path ->
// {policy} or policy string. fallback: published policy or ''.
function resolveConfigPolicyRule(path, rules, fallback) {
  const read = function (key) {
    const value = rules.get(key);
    return value === undefined ? '' : (typeof value === 'string' ? value : value.policy);
  };
  if (rules.has(path)) return { policy: read(path), source: 'file', rulePath: path };
  const parts = path.split('/');
  for (let end = parts.length - 1; end >= 1; end--) {
    const folder = parts.slice(0, end).join('/') + '/';
    if (rules.has(folder)) return { policy: read(folder), source: 'folder', rulePath: folder };
  }
  if (fallback) return { policy: fallback, source: 'published', rulePath: '' };
  return { policy: 'default_once', source: 'default', rulePath: '' };
}

// Own rules that still make sense to save: folder rules always (future files
// take them), file rules only for configs present in the effective profile.
function prunedOwnConfigPolicyRules(ownRules, effectivePaths) {
  const out = [];
  ownRules.forEach(function (policy, path) {
    if (!CONFIG_POLICY_LABELS[policy]) return;
    if (!path.endsWith('/') && !effectivePaths.has(path)) return;
    out.push({ path: path, policy: policy });
  });
  out.sort(function (a, b) { return a.path.localeCompare(b.path); });
  return out;
}

if (typeof module !== 'undefined') {
  module.exports = { CONFIG_POLICY_LABELS, isConfigPolicyPath, folderRulePath, mergeConfigPolicyLayers, resolveConfigPolicyRule, prunedOwnConfigPolicyRules };
}

function effectiveConfigPolicyRules() {
  const merged = new Map(state.inheritedPolicyRules);
  state.ownPolicyRules.forEach(function (policy, path) { merged.set(path, { policy: policy, origin: '' }); });
  return merged;
}

function configPolicyFor(path, parentEntry) {
  return resolveConfigPolicyRule(path, effectiveConfigPolicyRules(), parentEntry && parentEntry.policy ? parentEntry.policy : '');
}

async function fetchConfigPolicyRules(profileIdValue) {
  try {
    const payload = await request('/v1/admin/profiles/' + encodeURIComponent(profileIdValue) + '/config-policies');
    return Array.isArray(payload && payload.rules) ? payload.rules : [];
  } catch (error) {
    // Older servers (404/501) and test profiles (403) have no rules.
    if (error instanceof ApiError && [403, 404, 501].includes(error.status)) return [];
    throw error;
  }
}

// chain: resolved revisions from the nearest to the root (resolveEffective).
async function loadChainConfigPolicyRules(chain) {
  const layers = [];
  for (const item of chain.slice().reverse()) {
    const id = item && item.manifest && item.manifest.profile ? item.manifest.profile.id : '';
    if (!id) continue;
    layers.push({ origin: id, rules: await fetchConfigPolicyRules(id) });
  }
  return mergeConfigPolicyLayers(layers);
}

// Called when the draft's mother changes (new profile) or an update starts.
async function loadDraftConfigPolicyRules(inheritedChain, ownProfileId) {
  state.inheritedPolicyRules = new Map();
  state.ownPolicyRules = new Map();
  try {
    state.inheritedPolicyRules = await loadChainConfigPolicyRules(inheritedChain);
    if (ownProfileId) {
      (await fetchConfigPolicyRules(ownProfileId)).forEach(function (rule) {
        if (CONFIG_POLICY_LABELS[rule.policy]) state.ownPolicyRules.set(rule.path, rule.policy);
      });
    }
  } catch (error) {
    setStatus(nodes.folderStatus, 'No se pudieron cargar las reglas de políticas por carpeta: ' + describeError(error) + '. Las políticas publicadas por archivo se conservan.', 'warning');
  }
}

function setOwnConfigPolicyRule(path, policy) {
  if (policy) state.ownPolicyRules.set(path, policy);
  else state.ownPolicyRules.delete(path);
  invalidatePrepared();
  recomputeDiff();
}

function setFolderConfigPolicy(directoryPath, policy) {
  const rulePath = folderRulePath(directoryPath);
  const nested = Array.from(state.ownPolicyRules.keys()).filter(function (path) { return path !== rulePath && path.startsWith(rulePath); });
  if (policy && nested.length && window.confirm(nested.length + ' archivo(s) o subcarpeta(s) de ' + rulePath + ' tienen su propia regla. ¿Quitarlas para que sigan a la carpeta? (Cancelar las conserva como excepciones.)')) {
    nested.forEach(function (path) { state.ownPolicyRules.delete(path); });
  }
  setOwnConfigPolicyRule(rulePath, policy);
}

function describeConfigPolicySource(resolved) {
  const label = CONFIG_POLICY_LABELS[resolved.policy];
  if (resolved.source === 'folder') return 'Según ' + resolved.rulePath + ': ' + label;
  if (resolved.source === 'file') return 'Heredada: ' + label;
  if (resolved.source === 'published') return 'Publicada: ' + label;
  return 'Por defecto: ' + label;
}

function makePolicyOption(select, value, text) {
  const option = document.createElement('option');
  option.value = value;
  option.textContent = text;
  select.appendChild(option);
}

// Per-file selector shown in the explorer row of a config.
function configPolicyFileControl(entry) {
  const parentEntry = entry.parent || null;
  const own = state.ownPolicyRules.get(entry.path) || '';
  const withoutOwn = new Map(effectiveConfigPolicyRules());
  if (own) {
    if (state.inheritedPolicyRules.has(entry.path)) withoutOwn.set(entry.path, state.inheritedPolicyRules.get(entry.path));
    else withoutOwn.delete(entry.path);
  }
  const automatic = resolveConfigPolicyRule(entry.path, withoutOwn, parentEntry && parentEntry.policy ? parentEntry.policy : '');
  const select = make('select', 'policy-select explorer-policy');
  select.setAttribute('aria-label', 'Política de ' + entry.path);
  select.title = 'Obligatoria: el launcher la reinstala en cada actualización. Valor inicial: solo se copia si el jugador no la tiene; luego es suya.';
  makePolicyOption(select, '', describeConfigPolicySource(automatic));
  makePolicyOption(select, 'enforced', 'Obligatoria (este archivo)');
  makePolicyOption(select, 'default_once', 'Valor inicial (este archivo)');
  select.value = own;
  select.dataset.policy = own || automatic.policy;
  select.addEventListener('change', function () { setOwnConfigPolicyRule(entry.path, select.value); });
  return select;
}

// Folder selector shown in the explorer row of a folder under config/.
function configPolicyFolderControl(directoryPath) {
  const rulePath = folderRulePath(directoryPath);
  const own = state.ownPolicyRules.get(rulePath) || '';
  const inherited = state.inheritedPolicyRules.get(rulePath);
  const select = make('select', 'policy-select explorer-policy folder-policy');
  select.setAttribute('aria-label', 'Política de la carpeta ' + rulePath);
  select.title = 'Se aplica a todos los archivos de la carpeta y sus subcarpetas, también a los que se añadan después. Una regla de archivo o de una subcarpeta gana.';
  makePolicyOption(select, '', inherited ? 'Carpeta heredada: ' + CONFIG_POLICY_LABELS[inherited.policy] : 'Carpeta: sin regla');
  makePolicyOption(select, 'enforced', 'Carpeta: Obligatoria');
  makePolicyOption(select, 'default_once', 'Carpeta: Valor inicial');
  select.value = own;
  select.addEventListener('click', function (event) { event.stopPropagation(); });
  select.addEventListener('change', function () { setFolderConfigPolicy(directoryPath, select.value); });
  return select;
}

async function saveConfigPolicyRules(profileIdValue, manifest) {
  const effective = new Set();
  draftWorkbenchMap().forEach(function (entry, path) { if (entry.kind === 'config') effective.add(path); });
  const rules = prunedOwnConfigPolicyRules(state.ownPolicyRules, effective);
  await request('/v1/admin/profiles/' + encodeURIComponent(profileIdValue) + '/config-policies', {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ rules: rules })
  });
  return rules.length;
}
