'use strict';

// Publication drafts compare against the previous version, but keep the original
// pinned base in the new manifest. Updates must not become new inheritance levels.
function updateOwnObject(path) {
  return Boolean(state.updateProfile && !state.updateProfile.baseMap.has(path));
}

function resetPublicationDraft() {
  state.updateProfile = null;
  state.selectedFiles.clear(); state.removedPaths.clear(); state.createdDirs.clear();
  state.policies.clear(); state.modIds.clear(); state.localConfigSettings.clear();
  state.pathProblems = []; state.folderSelected = false; state.snapshotMode = false;
  state.icon = null;
  if (state.iconURL) URL.revokeObjectURL(state.iconURL);
  state.iconURL = null;
  state.parentMap = new Map(); state.parentConfigSettings = new Map();
  state.parentRef = null; state.parentManifest = null; state.parentDepth = 0;
  ['#derive-local', '#mods-add', '#mods-remove', '#config-default', '#activate-update'].forEach(id => { qs(id).checked = true; });
  qs('#config-enforced').checked = false;
  qs('#profile-icon-preview').hidden = qs('#remove-profile-icon').hidden = true;
  ['#profile-name', '#release-notes', '#minecraft-version', '#neoforge-version', '#profile-id'].forEach(id => { qs(id).value = ''; });
  qs('#profile-name').readOnly = false;
  qs('#revision-id').value = generatedRevisionID(); qs('#revision-sequence').value = '1';
  nodes.parentProfile.disabled = false; nodes.parentProfile.value = '';
  nodes.parentRevision.disabled = true;
  qs('#update-context').hidden = true;
  qs('#create-title').textContent = 'Nuevo perfil global';
  qs('#create-lede').textContent = 'Crea un perfil global para Wachiland Launcher.';
  nodes.publishButton.textContent = 'Publicar versión';
  invalidatePrepared();
}

async function startProfileUpdate(profile) {
  if ((state.selectedFiles.size || state.staged || state.updateProfile) && !window.confirm('¿Descartar el borrador actual y preparar esta actualización?')) return;
  window.location.hash = 'crear';
  setStatus(nodes.folderStatus, 'Cargando la versión publicada…');
  nodes.stageButton.disabled = true;
  try {
    const versions = await fetchProfileRevisions(profileId(profile));
    versions.sort((a, b) => (revisionSequence(b) || 0) - (revisionSequence(a) || 0));
    if (!versions.length) throw new Error('Este perfil aún no tiene una versión publicada.');
    const resolved = await resolveEffective(profileId(profile), revisionId(versions[0]), 0, new Set());
    const base = resolved.ancestors[0] || null;
    resetPublicationDraft();
    state.updateProfile = { profile: profile, resolved: resolved, baseMap: base ? base.map : new Map() };
    state.parentMap = new Map(resolved.map);
    state.parentConfigSettings = new Map(base ? base.configSettings : []);
    state.localConfigSettings = new Map((resolved.manifest.config_settings || []).map(rule => [configRuleID(rule.path, rule.key), { ...rule }]));
    state.parentRef = resolved.ref;
    state.parentManifest = base ? base.manifest : null;
    state.parentDepth = resolved.inheritanceDepth;
    state.folderSelected = true;
    resolved.map.forEach((entry, path) => { if (entry.kind === 'mod') state.modIds.set(path, entry.id); });
    qs('#profile-id').value = profileId(profile);
    qs('#profile-name').value = profileName(profile); qs('#profile-name').readOnly = true;
    qs('#release-notes').value = resolved.manifest.profile.description || '';
    qs('#minecraft-version').value = resolved.manifest.game.minecraft;
    updateNeoForgeOptions(); qs('#neoforge-version').value = resolved.manifest.game.neoforge;
    qs('#revision-sequence').value = String(Math.max(...versions.map(v => revisionSequence(v) || 0)) + 1);
    const permissions = resolved.manifest.permissions;
    qs('#derive-local').checked = permissions.derive_local;
    qs('#mods-add').checked = permissions.mods.add; qs('#mods-remove').checked = permissions.mods.remove;
    qs('#config-enforced').checked = permissions.configs.override_enforced;
    qs('#config-default').checked = permissions.configs.override_default_once;
    nodes.parentProfile.value = resolved.manifest.base ? resolved.manifest.base.profile_id : '';
    nodes.parentProfile.disabled = nodes.parentRevision.disabled = true;
    nodes.parentRevision.replaceChildren(new Option(resolved.manifest.base ? resolved.manifest.base.revision_id : 'Sin madre global', ''));
    qs('#create-title').textContent = 'Publicar actualización';
    qs('#create-lede').textContent = 'Añade o reemplaza archivos, revisa los cambios y publica una nueva versión del mismo perfil.';
    qs('#update-context').hidden = false;
    qs('#update-context-title').textContent = profileName(profile) + ' · versión ' + resolved.sequence + ' → ' + qs('#revision-sequence').value;
    qs('#update-context-help').textContent = 'Los archivos actuales ya están cargados. «Añadir archivos» modifica solo lo elegido; «Importar carpeta» compara un pack completo. La madre global y las versiones anteriores se conservan.';
    nodes.publishButton.textContent = 'Publicar actualización';
    setStatus(nodes.parentStatus, resolved.manifest.base ? 'Madre conservada: ' + resolved.manifest.base.profile_id + ' / ' + resolved.manifest.base.revision_id : 'Perfil raíz: la nueva versión seguirá siendo independiente.');
    setStatus(nodes.folderStatus, resolved.map.size + ' archivos publicados cargados. Puedes editar, reemplazar, añadir o quitar archivos.');
    await recomputeDiff();
  } catch (error) { setStatus(nodes.folderStatus, describeError(error), 'error'); }
  finally { nodes.stageButton.disabled = false; }
}

function buildProfileUpdateManifest() {
  if (state.updateProfile.published) throw new Error('Esta actualización ya se publicó. Abre de nuevo «Publicar actualización» para preparar la siguiente.');
  if (state.diff.some(entry => entry.status === 'unsupported')) throw new Error('Corrige las rutas no compatibles antes de publicar.');
  const update = state.updateProfile;
  const manifest = JSON.parse(JSON.stringify(update.resolved.manifest));
  const base = update.baseMap;
  manifest.revision = { id: requiredValue('#revision-id', 'el ID de revisión'), sequence: Number(qs('#revision-sequence').value), created_at: new Date().toISOString() };
  if (!Number.isSafeInteger(manifest.revision.sequence) || manifest.revision.sequence <= update.resolved.sequence) throw new Error('La nueva versión debe avanzar la secuencia del perfil.');
  const minecraft = requiredValue('#minecraft-version', 'Minecraft');
  const neoforge = requiredValue('#neoforge-version', 'NeoForge');
  if (!state.minecraftVersions.includes(minecraft) || !allowedNeoForgeVersions().includes(neoforge)) throw new Error('Selecciona versiones compatibles del catálogo.');
  manifest.game = { minecraft: minecraft, neoforge: neoforge };
  if (manifest.base && (minecraft !== update.resolved.manifest.game.minecraft || neoforge !== update.resolved.manifest.game.neoforge)) throw new Error('Un perfil derivado debe conservar las versiones de juego de su madre.');
  manifest.profile.description = qs('#release-notes').value.trim();
  if (state.icon) manifest.profile.icon = { sha256: state.icon.sha256, size: state.icon.file.size, media_type: 'image/png' };
  manifest.permissions = { derive_local: qs('#derive-local').checked, mods: { add: qs('#mods-add').checked, remove: qs('#mods-remove').checked }, configs: { override_enforced: qs('#config-enforced').checked, override_default_once: qs('#config-default').checked }, max_inheritance_depth: 8 };
  ['mods', 'configs', 'objects', 'remove_mods', 'remove_configs'].forEach(key => { manifest[key] ||= []; });
  state.diff.forEach(entry => {
    const inherited = base.get(entry.path);
    manifest.mods = manifest.mods.filter(item => item.path !== entry.path);
    manifest.configs = manifest.configs.filter(item => item.path !== entry.path);
    manifest.objects = manifest.objects.filter(item => item.path !== entry.path);
    manifest.remove_configs = manifest.remove_configs.filter(item => item.path !== entry.path);
    const id = entry.kind === 'mod' ? (inherited ? inherited.id : entry.parent && entry.parent.id) : null;
    if (id) manifest.remove_mods = manifest.remove_mods.filter(item => item.id !== id);
    if (entry.status === 'removed') {
      if (!inherited) return; // Removing this profile's own file needs no base tombstone.
      if (inherited.kind === 'mod') manifest.remove_mods.push({ id: inherited.id, expect_base_object_sha256: objectDigest(inherited) });
      else if (inherited.kind === 'config') manifest.remove_configs.push({ path: entry.path, expect_base_object_sha256: objectDigest(inherited) });
      else throw new Error('No se puede retirar el objeto heredado ' + entry.path + '.');
      return;
    }
    const item = { path: entry.path, object: objectRef(entry.local) };
    if (inherited) item.expect_base_object_sha256 = objectDigest(inherited);
    if (entry.kind === 'mod') { item.id = state.modIds.get(entry.path) || defaultModId(entry.path); manifest.mods.push(item); }
    else if (entry.kind === 'config') { item.policy = policyFor(entry); manifest.configs.push(item); }
    else { item.id = entry.parent && entry.parent.id || defaultObjectId(entry.path); manifest.objects.push(item); }
  });
  const effective = draftWorkbenchMap();
  manifest.config_settings = Array.from(state.localConfigSettings.values()).filter(rule => effective.has(rule.path));
  for (const rule of manifest.config_settings) if ((effective.get(rule.path) || {}).kind !== 'config') throw new Error('La regla ' + rule.key + ' debe apuntar a una configuración presente en el pack.');
  manifest.schema_version = manifest.config_settings.length ? 2 : 1;
  return manifest;
}

function draftWorkbenchMap() {
  const map = new Map(state.parentMap);
  if (state.snapshotMode) map.clear();
  state.selectedFiles.forEach((entry, path) => map.set(path, { path: path, kind: entry.kind, file: entry.file, object: objectRef(entry) }));
  state.removedPaths.forEach(path => map.delete(path));
  return map;
}

async function openDraftWorkbench(path) {
  const resolved = state.updateProfile ? state.updateProfile.resolved : null;
  let ancestors = resolved ? [resolved].concat(resolved.ancestors) : [];
  if (!resolved && state.parentRef) {
    try {
      const parent = await resolveEffective(state.parentRef.profile_id, state.parentRef.revision_id, 0, new Set());
      ancestors = [parent].concat(parent.ancestors);
    } catch (error) { setStatus(nodes.folderStatus, describeError(error), 'error'); return; }
  }
  showProfileWorkbench('Borrador · ' + qs('#profile-name').value, draftWorkbenchMap(), ancestors.slice(0, state.updateProfile ? 9 : 8).map((item, index) => ({ label: (state.updateProfile && index === 0 ? 'Versión anterior' : 'Antepasado ' + (state.updateProfile ? index : index + 1)) + ' · ' + item.manifest.profile.name, resolved: item })), path);
}

async function openPublishedWorkbench(profile) {
  const dialog = make('dialog', 'profile-comparison-dialog');
  const loading = make('p', 'inline-status', 'Cargando archivos y antepasados…');
  const close = make('button', 'button button-secondary', 'Cerrar'); close.addEventListener('click', () => dialog.close());
  dialog.append(loading, close); document.body.appendChild(dialog); dialog.showModal();
  dialog.addEventListener('close', () => dialog.remove());
  try {
    const versions = await fetchProfileRevisions(profileId(profile));
    versions.sort((a, b) => (revisionSequence(b) || 0) - (revisionSequence(a) || 0));
    if (!versions.length) throw new Error('El perfil no tiene revisiones.');
    const resolved = await resolveEffective(profileId(profile), revisionId(versions[0]), 0, new Set());
    if (!dialog.open) return;
    const choices = resolved.ancestors.slice(0, 8).map((item, index) => ({ label: (index === 0 ? 'Madre' : index === 1 ? 'Abuela' : 'Antepasado ' + (index + 1)) + ' · ' + item.manifest.profile.name, resolved: item }));
    versions.slice(1).forEach(version => choices.push({ label: 'Versión anterior ' + revisionSequence(version), profile: profileId(profile), revision: revisionId(version) }));
    dialog.close();
    showProfileWorkbench(profileName(profile) + ' · versión ' + resolved.sequence, resolved.map, choices);
  } catch (error) { loading.textContent = describeError(error); }
}

async function workbenchText(entry, cache) {
  if (!entry) return '';
  if (entry.file) { if (entry.file.size > 1048576) throw new Error('El visor admite hasta 1 MiB por archivo.'); return entry.file.text(); }
  const hash = objectDigest(entry);
  if (cache.has(hash)) return cache.get(hash);
  if (!hash || entry.object.size > 1048576) throw new Error('El visor admite hasta 1 MiB por archivo.');
  const response = await fetch('/v1/objects/sha256/' + encodeURIComponent(hash), { credentials: 'same-origin' });
  if (!response.ok) throw new Error('No se pudo leer el archivo: HTTP ' + response.status);
  const bytes = await response.arrayBuffer();
  if (bytes.byteLength > 1048576) throw new Error('El archivo supera 1 MiB.');
  if (await sha256Bytes(bytes) !== hash) throw new Error('El archivo no coincide con su SHA-256.');
  const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  if (text.includes('\0')) throw new Error('Este archivo es binario.');
  if (cache.size >= 32) cache.clear(); cache.set(hash, text); return text;
}

async function workbenchLineDiff(left, right, isCurrent) {
  const a = left ? left.match(/[^\n]*\n|[^\n]+$/g) || [] : [];
  const b = right ? right.match(/[^\n]*\n|[^\n]+$/g) || [] : [];
  const w = b.length + 1;
  const table = (a.length + 1) * w <= 1500000 ? new Uint32Array((a.length + 1) * w) : null;
  if (table) for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) table[i * w + j] = a[i] === b[j] ? 1 + table[(i + 1) * w + j + 1] : Math.max(table[(i + 1) * w + j], table[i * w + j + 1]);
    if (i % 64 === 0) { await new Promise(resolve => setTimeout(resolve, 0)); if (!isCurrent()) return []; }
  }
  let i = 0, j = 0, removed = [], added = []; const rows = [];
  function flush() { for (let k = 0; k < Math.max(removed.length, added.length); k++) rows.push({ left: removed[k], right: added[k], changed: true }); removed = []; added = []; }
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) { flush(); rows.push({ left: [i + 1, a[i++]], right: [j + 1, b[j++]], changed: false }); }
    else {
      let takeLeft = j === b.length;
      if (!takeLeft && i < a.length) {
        if (table) takeLeft = table[(i + 1) * w + j] >= table[i * w + j + 1];
        else { let nextA = a.slice(i + 1, i + 65).indexOf(b[j]), nextB = b.slice(j + 1, j + 65).indexOf(a[i]); takeLeft = (nextA < 0 ? 65 : nextA) <= (nextB < 0 ? 65 : nextB); }
      }
      if (takeLeft) removed.push([i + 1, a[i++]]); else added.push([j + 1, b[j++]]);
    }
    if ((i + j) % 512 === 0) { await new Promise(resolve => setTimeout(resolve, 0)); if (!isCurrent()) return []; }
  }
  flush(); return rows;
}

function showProfileWorkbench(title, currentMap, choices, initialPath) {
  const dialog = make('dialog', 'profile-comparison-dialog');
  const heading = make('div', 'comparison-heading');
  const close = make('button', 'button button-secondary', 'Cerrar'); close.addEventListener('click', () => dialog.close());
  heading.append(make('h2', '', title), close);
  const workspace = make('div', 'comparison-workspace');
  const sidebar = make('aside', 'comparison-explorer');
  const search = make('input'); search.placeholder = 'Buscar archivo…'; search.setAttribute('aria-label', 'Buscar archivo');
  const onlyChanges = make('label', 'check-row'); const check = make('input'); check.type = 'checkbox';
  onlyChanges.append(check, make('span', '', 'Solo diferencias'));
  const tree = make('div', 'comparison-tree'); sidebar.append(search, onlyChanges, tree);
  const detail = make('section', 'comparison-detail');
  const info = make('p', 'comparison-status', 'Selecciona un archivo para comparar.');
  const selector = make('select'); selector.setAttribute('aria-label', 'Comparar con antepasado o revisión');
  choices.forEach((choice, index) => selector.add(new Option(choice.label, String(index))));
  if (!choices.length) selector.add(new Option('Sin antepasado ni versión anterior', '')); selector.disabled = !choices.length;
  const columns = make('div', 'comparison-columns');
  const left = make('div', 'comparison-column'), right = make('div', 'comparison-column');
  const leftHeader = make('strong', 'comparison-column-title', 'Este perfil / borrador');
  const rightHeader = make('div', 'comparison-column-title'); rightHeader.appendChild(selector);
  const leftCode = make('div', 'comparison-code'), rightCode = make('div', 'comparison-code');
  left.append(leftHeader, leftCode); right.append(rightHeader, rightCode);
  const splitter = make('div', 'comparison-splitter'); splitter.tabIndex = 0; splitter.setAttribute('role', 'separator'); splitter.setAttribute('aria-orientation', 'vertical'); splitter.setAttribute('aria-label', 'Anchura de las columnas');
  const overview = make('div', 'comparison-overview');
  columns.append(left, splitter, right, overview); detail.append(info, columns); workspace.append(sidebar, detail);
  dialog.append(heading, make('p', 'microcopy', 'Archivos publicados: verdes a la izquierda, rojos a la derecha. Las reglas por parámetro se gestionan en el editor de configuración.'), workspace);
  document.body.appendChild(dialog); dialog.showModal();
  let baseline = new Map(), selected = initialPath || '', generation = 0, synchronizing = false;
  const cache = new Map();
  dialog.addEventListener('close', () => { generation++; cache.clear(); dialog.remove(); });
  [leftCode, rightCode].forEach((pane, index) => pane.addEventListener('scroll', () => {
    if (synchronizing) return; synchronizing = true;
    (index ? leftCode : rightCode).scrollTop = pane.scrollTop;
    requestAnimationFrame(() => { synchronizing = false; });
  }));
  function splitAt(ratio) { const bounded = Math.max(0.2, Math.min(0.8, ratio)); columns.style.setProperty('--comparison-split', bounded * 100 + '%'); splitter.setAttribute('aria-valuenow', Math.round(bounded * 100)); }
  splitter.addEventListener('pointerdown', event => { if (event.button !== 0) return; splitter.setPointerCapture(event.pointerId); });
  splitter.addEventListener('pointermove', event => { if (!splitter.hasPointerCapture(event.pointerId)) return; const bounds = columns.getBoundingClientRect(); splitAt((event.clientX - bounds.left) / (bounds.width - 20)); });
  splitter.addEventListener('pointerup', event => { if (splitter.hasPointerCapture(event.pointerId)) splitter.releasePointerCapture(event.pointerId); });
  splitter.addEventListener('keydown', event => { if (!['ArrowLeft', 'ArrowRight', 'Home'].includes(event.key)) return; event.preventDefault(); const ratio = Number(splitter.getAttribute('aria-valuenow') || 50) / 100; splitAt(event.key === 'Home' ? 0.5 : ratio + (event.key === 'ArrowLeft' ? -0.05 : 0.05)); });
  function status(path) { const local = currentMap.get(path), parent = baseline.get(path); return !local ? 'removed' : !parent ? 'added' : objectDigest(local) !== objectDigest(parent) ? 'changed' : 'unchanged'; }
  function renderTree() {
    tree.replaceChildren(); const folders = new Map([['', tree]]);
    const paths = Array.from(new Set([...currentMap.keys(), ...baseline.keys()])).sort();
    for (const path of paths) {
      const change = status(path);
      if (!path.toLowerCase().includes(search.value.toLowerCase()) || (check.checked && change === 'unchanged')) continue;
      let prefix = ''; let host = tree; const parts = path.split('/');
      for (const part of parts.slice(0, -1)) {
        prefix = prefix ? prefix + '/' + part : part;
        if (!folders.has(prefix)) { const folder = make('details', 'comparison-folder'); folder.open = Boolean(search.value || initialPath && initialPath.startsWith(prefix + '/')); const summary = make('summary', '', part); const body = make('div'); folder.append(summary, body); host.appendChild(folder); folders.set(prefix, body); }
        host = folders.get(prefix);
      }
      const button = make('button', 'comparison-file change-' + change + (path === selected ? ' selected' : ''), parts[parts.length - 1]);
      button.type = 'button'; button.title = path + ' · ' + (change === 'unchanged' ? 'Sin cambios' : changeLabel(change));
      button.addEventListener('click', () => { selected = path; renderTree(); renderFile(); }); host.appendChild(button);
    }
    if (!tree.childElementCount) tree.appendChild(make('p', 'microcopy', 'No hay archivos que coincidan.'));
  }
  async function renderFile() {
    const token = ++generation; leftCode.replaceChildren(); rightCode.replaceChildren(); overview.replaceChildren();
    if (!selected) { info.textContent = 'Selecciona un archivo del explorador para comparar su contenido.'; return; }
    info.textContent = selected + ' · cargando…';
    if (!isEditableText(selected, '')) { info.textContent = selected + ' · ' + (status(selected) === 'unchanged' ? 'Sin cambios' : changeLabel(status(selected))) + '. Archivo binario; se compara por SHA-256.'; return; }
    try {
      const texts = await Promise.all([workbenchText(currentMap.get(selected), cache), workbenchText(baseline.get(selected), cache)]);
      const rows = await workbenchLineDiff(texts[0], texts[1], () => generation === token && dialog.open);
      if (generation !== token || !dialog.open) return;
      let changed = 0; const fragments = [document.createDocumentFragment(), document.createDocumentFragment()];
      const visibleRows = rows.slice(0, 10000);
      for (let index = 0; index < visibleRows.length; index++) {
        const row = rows[index]; if (row.changed) changed++;
        ['left', 'right'].forEach((side, sideIndex) => { const line = row[side]; const element = make('div', 'comparison-line' + (row.changed && line ? sideIndex ? ' diff-removed' : ' diff-added' : '')); element.append(make('span', 'comparison-line-number', line ? line[0] : ''), make('code', '', line ? line[1].replace(/[\r\n]+$/, '') : '')); fragments[sideIndex].appendChild(element); });
        if (row.changed) { const marker = make('button', 'comparison-marker' + (row.left ? ' marker-local' : '') + (row.right ? ' marker-parent' : '')); marker.type = 'button'; marker.style.top = index / Math.max(rows.length, 1) * 100 + '%'; marker.title = 'Ir al cambio ' + (index + 1); marker.addEventListener('click', () => { leftCode.scrollTop = rightCode.scrollTop = index * 24; }); overview.appendChild(marker); }
        if (index % 300 === 0) { await new Promise(resolve => setTimeout(resolve, 0)); if (generation !== token) return; }
      }
      leftCode.appendChild(fragments[0]); rightCode.appendChild(fragments[1]); leftCode.scrollTop = rightCode.scrollTop = 0;
      info.textContent = selected + ' · ' + (changed ? changed + ' líneas diferentes' : 'Sin diferencias con la referencia seleccionada') + (rows.length > visibleRows.length ? ' · mostrando las primeras 10.000 líneas' : '');
    } catch (error) { if (generation === token) info.textContent = selected + ' · ' + describeError(error); }
  }
  async function chooseReference() {
    const token = ++generation; info.textContent = 'Cargando referencia…';
    try {
      const choice = choices[Number(selector.value)];
      if (choice && !choice.resolved) choice.resolved = await resolveEffective(choice.profile, choice.revision, 0, new Set());
      if (generation !== token || !dialog.open) return;
      baseline = choice ? choice.resolved.map : new Map(); renderTree(); renderFile();
    } catch (error) { if (generation === token) info.textContent = describeError(error); }
  }
  selector.addEventListener('change', chooseReference); search.addEventListener('input', renderTree); check.addEventListener('change', renderTree);
  chooseReference();
}

qs('#review-update').addEventListener('click', () => openDraftWorkbench());
qs('#cancel-update').addEventListener('click', async () => {
  if (!window.confirm('¿Descartar el borrador de actualización? Las versiones publicadas no se modificarán.')) return;
  resetPublicationDraft(); await handleParentProfileChange();
});
document.querySelectorAll('a[href="#crear"]').forEach(link => link.addEventListener('click', async event => {
  if (!state.updateProfile) return;
  if (!state.updateProfile.published && !window.confirm('¿Descartar el borrador de actualización y crear otro perfil?')) { event.preventDefault(); return; }
  resetPublicationDraft(); await handleParentProfileChange();
}));
