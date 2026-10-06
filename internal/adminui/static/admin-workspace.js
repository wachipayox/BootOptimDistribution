'use strict';

// Presentation only: publication, signature and pinned-inheritance validation
// continue through the existing API and draft functions.
const workspaceState = { manifests: new Map(), parents: new Map(), loading: false, token: 0, step: 1, search: '' };

function profileLink(id, tab) { return '#perfil/' + encodeURIComponent(id) + '/' + (tab || 'resumen'); }
function activeProfileRef(profile) {
  const stable = (profile.channels || []).find(channel => (channel.channel || channel.name) === 'stable');
  return stable || null;
}
function profileIcon(profile, className) {
  const icon = profile.presentation ? profile.presentation.icon : profile.icon;
  const holder = make('div', className || 'library-icon');
  if (icon && /^[a-f0-9]{64}$/.test(icon.sha256 || '')) {
    const image = make('img'); image.alt = ''; image.loading = 'lazy';
    image.src = '/v1/objects/sha256/' + icon.sha256;
    image.addEventListener('error', () => { image.remove(); holder.textContent = profileName(profile).slice(0, 1); });
    holder.appendChild(image);
  } else holder.textContent = profileName(profile).slice(0, 1);
  return holder;
}
function profileDescription(profile) { return (profile.presentation ? profile.presentation.description : profile.description) || 'Sin descripción todavía.'; }
function workspaceButton(label, action, primary) {
  const button = make('button', 'button ' + (primary ? 'button-primary' : 'button-secondary'), label);
  button.type = 'button'; button.addEventListener('click', action); return button;
}
async function latestProfileManifest(profile) {
  const ref = profile.latest_revision;
  if (!ref || !revisionId(ref)) return null;
  const key = profileId(profile) + '/' + revisionId(ref);
  if (!workspaceState.manifests.has(key)) {
    const promise = fetchRevision(profileId(profile), revisionId(ref)).then(payload => parseManifest(unwrapEnvelope(payload)));
    workspaceState.manifests.set(key, promise);
    promise.catch(() => workspaceState.manifests.delete(key));
  }
  return workspaceState.manifests.get(key);
}
async function loadLibraryLineage() {
  if (workspaceState.loading) return;
  const missing = state.profiles.filter(profile => profile.latest_revision && !workspaceState.parents.has(profileId(profile) + '/' + revisionId(profile.latest_revision)));
  if (!missing.length) return;
  workspaceState.loading = true;
  // Four small manifest requests at a time; never read pack objects for cards.
  let index = 0;
  await Promise.all(Array.from({ length: Math.min(4, missing.length) }, async () => {
    while (index < missing.length) {
      const profile = missing[index++];
      try {
        const manifest = await latestProfileManifest(profile);
        workspaceState.parents.set(profileId(profile) + '/' + revisionId(profile.latest_revision), manifest && manifest.base ? manifest.base.profile_id : '');
      } catch (_) { /* A card remains accessible even if its lineage is unavailable. */ }
    }
  }));
  workspaceState.loading = false;
  renderLibraryProfiles(false);
}
function libraryParent(profile) { return workspaceState.parents.get(profileId(profile) + '/' + revisionId(profile.latest_revision)) || ''; }
function renderLibraryProfiles(loadLineage) {
  const query = workspaceState.search.trim().toLowerCase();
  const matches = profile => (profileName(profile) + ' ' + profileDescription(profile) + ' ' + profileId(profile)).toLowerCase().includes(query);
  const byId = new Map(state.profiles.map(profile => [profileId(profile), profile]));
  const children = new Map();
  state.profiles.forEach(profile => { const parent = libraryParent(profile); if (parent && byId.has(parent) && parent !== profileId(profile)) { if (!children.has(parent)) children.set(parent, []); children.get(parent).push(profile); } });
  const visited = new Set(); const grid = make('div', 'library-families');
  function containsMatch(profile, seen) {
    if (seen.has(profileId(profile))) return false;
    seen.add(profileId(profile));
    return matches(profile) || (children.get(profileId(profile)) || []).some(child => containsMatch(child, seen));
  }
  function card(profile, depth) {
    const id = profileId(profile); if (visited.has(id)) return null; visited.add(id);
    if (!containsMatch(profile, new Set())) return null;
    const item = make('article', 'library-card' + (depth ? ' library-child' : ''));
    const top = make('a', 'library-card-link'); top.href = profileLink(id);
    const copy = make('div'); copy.append(make('h3', '', profileName(profile)), make('p', 'library-description', profileDescription(profile)));
    top.append(profileIcon(profile), copy); item.appendChild(top);
    const meta = make('div', 'library-meta'); const active = activeProfileRef(profile);
    meta.append(make('span', 'mini-badge', active ? 'Activa · v' + revisionSequence(active) : 'Sin versión activa'), make('span', '', profile.minecraft ? 'Minecraft ' + profile.minecraft : 'Perfil global'));
    item.appendChild(meta);
    const actions = make('div', 'library-actions'); const open = make('a', 'text-link', 'Abrir perfil →'); open.href = profileLink(id);
    actions.append(open, workspaceButton('Actualizar', () => startProfileUpdate(profile), true)); item.appendChild(actions);
    const descendants = children.get(id) || [];
    if (descendants.length) {
      const branch = make('details', 'library-branches'); branch.open = Boolean(query);
      branch.appendChild(make('summary', '', descendants.length + (descendants.length === 1 ? ' rama derivada' : ' ramas derivadas')));
      const list = make('div', 'library-branch-list'); descendants.forEach(child => { const nested = card(child, depth + 1); if (nested) list.appendChild(nested); }); branch.appendChild(list); item.appendChild(branch);
    }
    return item;
  }
  const sorted = [...state.profiles].sort((a, b) => profileName(a).localeCompare(profileName(b), 'es'));
  sorted.filter(profile => !byId.has(libraryParent(profile))).forEach(profile => { const item = card(profile, 0); if (item) grid.appendChild(item); });
  sorted.filter(profile => !visited.has(profileId(profile))).forEach(profile => { const item = card(profile, 0); if (item) grid.appendChild(item); });
  nodes.profilesList.className = '';
  nodes.profilesList.replaceChildren(grid.childElementCount ? grid : make('div', 'workspace-empty', state.profiles.length ? 'No hay perfiles que coincidan con la búsqueda.' : 'Tu biblioteca está vacía. Crea tu primer perfil global.'));
  qs('#library-count').textContent = state.profiles.filter(matches).length + ' de ' + state.profiles.length + ' perfiles';
  if (loadLineage !== false) loadLibraryLineage();
  renderWorkspaceOverview();
  if (window.location.hash.startsWith('#perfil/')) renderProfilePage();
}
function renderWorkspaceOverview() {
  const host = nodes.overviewProfiles; const list = make('div', 'overview-library');
  state.profiles.slice(0, 5).forEach(profile => {
    const link = make('a', 'overview-profile'); link.href = profileLink(profileId(profile));
    const copy = make('div'); const active = activeProfileRef(profile);
    copy.append(make('strong', '', profileName(profile)), make('span', 'microcopy', active ? 'Versión activa ' + revisionSequence(active) : 'Sin versión activa'));
    link.append(profileIcon(profile), copy, make('span', '', '→')); list.appendChild(link);
  });
  host.className = ''; host.replaceChildren(list.childElementCount ? list : make('p', 'workspace-empty', 'Crea tu primer perfil para empezar.'));
  let stats = qs('#workspace-stats');
  if (!stats) { stats = make('div', 'workspace-stats'); stats.id = 'workspace-stats'; qs('[data-view="resumen"] .page-heading').after(stats); }
  stats.replaceChildren();
  [['Perfiles globales', state.profiles.length], ['Versiones activas', state.profiles.filter(profile => activeProfileRef(profile)).length], ['Versión del servicio', state.build.version || qs('#service-version').textContent]].forEach(([label, value]) => {
    const metric = make('div'); metric.append(make('span', 'microcopy', label), make('strong', '', value)); stats.appendChild(metric);
  });
}
function profileRouteParts() {
  const parts = window.location.hash.slice(1).split('/');
  let id = ''; try { id = decodeURIComponent(parts[1] || ''); } catch (_) { /* Invalid URL displays missing profile. */ }
  const tab = ['resumen', 'archivos', 'versiones', 'ajustes'].includes(parts[2]) ? parts[2] : 'resumen';
  return { id, tab };
}
async function renderProfilePage() {
  const token = ++workspaceState.token; const { id, tab } = profileRouteParts();
  const profile = state.profiles.find(item => profileId(item) === id); const host = qs('#profile-page');
  if (!profile) { host.replaceChildren(make('div', 'workspace-empty', state.profiles.length ? 'Este perfil ya no está en la biblioteca.' : 'Cargando biblioteca…')); return; }
  const crumbs = make('div', 'breadcrumbs'); const back = make('a', '', 'Biblioteca'); back.href = '#perfiles'; crumbs.append(back, make('span', '', '/'), make('span', '', profileName(profile)));
  const hero = make('header', 'profile-hero'); const copy = make('div', 'profile-hero-copy');
  const title = make('h2', '', profileName(profile)); title.id = 'profile-page-title';
  copy.append(make('p', 'eyebrow', 'PERFIL GLOBAL'), title, make('p', 'lede', profileDescription(profile)));
  const active = activeProfileRef(profile); const badges = make('div', 'library-meta');
  badges.append(make('span', 'mini-badge', active ? 'Activa · versión ' + revisionSequence(active) : 'Sin versión activa'), make('span', '', 'Minecraft ' + (profile.minecraft || '—')), make('span', '', 'NeoForge ' + (profile.neoforge || '—'))); copy.appendChild(badges);
  hero.append(profileIcon(profile, 'profile-hero-icon'), copy, workspaceButton('Publicar actualización', () => startProfileUpdate(profile), true));
  const tabs = make('nav', 'profile-tabs'); tabs.setAttribute('aria-label', 'Secciones del perfil');
  [['resumen', 'Resumen'], ['archivos', 'Archivos y cambios'], ['versiones', 'Versiones'], ['ajustes', 'Ajustes']].forEach(([value, label]) => { const link = make('a', '', label); link.href = profileLink(id, value); if (value === tab) link.setAttribute('aria-current', 'page'); tabs.appendChild(link); });
  const body = make('div', 'profile-tab-body'); body.appendChild(make('p', 'inline-status', 'Cargando ' + (tab === 'archivos' ? 'archivos y referencias…' : 'información del perfil…')));
  host.replaceChildren(crumbs, hero, tabs, body);
  const valid = () => token === workspaceState.token && window.location.hash.startsWith('#perfil/') && profileRouteParts().id === id && profileRouteParts().tab === tab;
  try {
    if (tab === 'ajustes') {
      body.replaceChildren(); const presentation = make('section', 'panel settings-section');
      presentation.append(make('h3', '', 'Presentación del perfil'), make('p', 'microcopy', 'Nombre, descripción e icono del launcher. Estos cambios no publican una versión del modpack.'), workspaceButton('Editar presentación', () => editProfilePresentation(profile)));
      const danger = make('details', 'panel danger-zone'); danger.appendChild(make('summary', '', 'Zona de eliminación'));
      danger.append(make('p', 'microcopy', 'Retira el perfil del catálogo. El historial se conserva para las instancias y ramas existentes.'), workspaceButton('Borrar perfil', () => deleteGlobalProfile(profile)));
      body.append(presentation, danger); return;
    }
    const versions = await fetchProfileRevisions(id); versions.sort((a, b) => (revisionSequence(b) || 0) - (revisionSequence(a) || 0));
    if (!valid()) return;
    if (tab === 'versiones') {
      body.replaceChildren(make('div', 'section-heading', 'Historial de versiones · ' + versions.length));
      const revisions = make('div', 'workspace-versions'); renderProfileRevisions(id, revisions, versions); body.appendChild(revisions); return;
    }
    if (!versions.length) { body.replaceChildren(make('p', 'workspace-empty', 'Este perfil no tiene versiones publicadas.')); return; }
    if (tab === 'archivos') {
      const resolved = await resolveEffective(id, revisionId(versions[0]), 0, new Set()); if (!valid()) return;
      const choices = resolved.ancestors.slice(0, 8).map((item, index) => ({ label: (index === 0 ? 'Madre' : index === 1 ? 'Abuela' : 'Antepasado ' + (index + 1)) + ' · ' + item.manifest.profile.name, resolved: item }));
      versions.slice(1).forEach(version => choices.push({ label: 'Versión anterior ' + revisionSequence(version), profile: id, revision: revisionId(version) }));
      showProfileWorkbench('Archivos · versión ' + resolved.sequence, resolved.map, choices, '', body); return;
    }
    const manifest = await latestProfileManifest(profile); if (!valid()) return;
    body.replaceChildren(); const grid = make('div', 'profile-summary-grid');
    const releases = make('section', 'panel'); releases.append(make('p', 'eyebrow', 'PUBLICACIONES'), make('h3', '', 'Última publicada · versión ' + revisionSequence(versions[0])), make('p', 'microcopy', active ? 'Los launchers siguen la versión ' + revisionSequence(active) + ' del canal estable.' : 'Elige una versión en la pestaña Versiones para activarla en los launchers.'), make('p', 'microcopy', versions.length + ' versiones en el historial.'));
    const lineage = make('section', 'panel'); lineage.append(make('p', 'eyebrow', 'FAMILIA'), make('h3', '', 'Madre y ramas derivadas'));
    if (manifest && manifest.base) {
      const parent = state.profiles.find(item => profileId(item) === manifest.base.profile_id); const link = make('a', 'family-link', parent ? profileName(parent) : manifest.base.profile_id);
      if (parent) link.href = profileLink(profileId(parent)); lineage.append(make('span', 'microcopy', 'Basado en '), link, make('p', 'microcopy', 'La revisión madre está fijada; no cambia al publicar una actualización.'));
    } else lineage.appendChild(make('p', 'microcopy', 'Perfil raíz. No hereda de otro perfil.'));
    const descendants = state.profiles.filter(item => libraryParent(item) === id);
    descendants.forEach(child => { const link = make('a', 'family-link', '↳ ' + profileName(child)); link.href = profileLink(profileId(child)); lineage.appendChild(link); });
    if (!descendants.length) lineage.appendChild(make('p', 'microcopy', 'Sin ramas derivadas publicadas.'));
    const detail = make('section', 'panel profile-identity'); detail.append(make('h3', '', 'Identidad del perfil'), make('code', '', id), make('p', 'microcopy', 'La identidad y las versiones publicadas se conservan. Cambia la presentación desde Ajustes.'));
    grid.append(releases, lineage, detail); body.appendChild(grid);
  } catch (error) { if (token === workspaceState.token) body.replaceChildren(make('div', 'alert alert-error', describeError(error)), workspaceButton('Reintentar', renderProfilePage)); }
}

function workspaceRoute(target) {
  workspaceState.token++;
  const labels = { resumen: 'Inicio', perfiles: 'Biblioteca', perfil: 'Biblioteca / Perfil', crear: 'Publicación', servicio: 'Servicio' };
  qs('.topbar-label').textContent = 'Distribución / ' + labels[target];
  document.body.dataset.workspaceView = target;
  if (target === 'perfil') renderProfilePage();
  if (target === 'crear') syncPublicationWizard();
}

// Keep a single live draft in the DOM while revealing one publication step.
const identityPanel = qs('#identity-title').closest('.panel');
const filesPanel = qs('#folder-title').closest('.panel');
const publishPanel = qs('#publish-title').closest('.panel');
const reviewPanel = make('section', 'panel publication-review');
reviewPanel.append(make('p', 'eyebrow', 'PASO 2'), make('h3', '', 'Revisa qué recibirá el launcher'), make('p', 'microcopy', 'Comprueba las altas, reemplazos y eliminaciones antes de subir archivos.'));
const diffHeading = qs('#diff-title').closest('.step-subheading');
let diffNode = diffHeading;
while (diffNode) { const next = diffNode.nextElementSibling; reviewPanel.appendChild(diffNode); diffNode = next; }
const reviewActions = make('div', 'card-actions'); reviewActions.appendChild(workspaceButton('Abrir comparación detallada', () => openDraftWorkbench())); reviewPanel.appendChild(reviewActions);
filesPanel.after(reviewPanel);
const permissions = make('details', 'publication-permissions'); permissions.appendChild(make('summary', '', 'Permisos para las ramas derivadas')); permissions.appendChild(qs('.workflow-side .panel')); reviewPanel.appendChild(permissions);
qs('.workflow-side').remove();
const identityDetails = make('details', 'publication-identity'); identityDetails.appendChild(make('summary', '', 'Información del perfil y versiones del juego')); identityPanel.before(identityDetails); identityDetails.appendChild(identityPanel);
const synthetic = nodes.loadSynthetic.closest('.callout'); const example = make('details', 'publication-example'); example.appendChild(make('summary', '', '¿Quieres probar con archivos de ejemplo?')); synthetic.before(example); example.appendChild(synthetic);
qs('#review-update').hidden = true;
const activationLabel = qs('#activate-update').closest('label');
publishPanel.querySelector('.publish-final').before(activationLabel);
function syncPublicationWizard() {
  const step = workspaceState.step;
  identityDetails.hidden = filesPanel.hidden = step !== 1;
  identityDetails.open = !state.updateProfile;
  reviewPanel.hidden = step !== 2; publishPanel.hidden = step !== 3;
  activationLabel.hidden = !state.updateProfile;
  qs('#wizard-back').hidden = step === 1; qs('#wizard-next').hidden = step === 3;
  qs('#wizard-next').textContent = step === 1 ? 'Revisar cambios →' : 'Continuar a publicación →';
  qs('#publication-steps').querySelectorAll('button').forEach(button => { const active = Number(button.dataset.step) === step; if (active) button.setAttribute('aria-current', 'step'); else button.removeAttribute('aria-current'); });
  const name = qs('#profile-name').value || 'Nuevo perfil'; const summary = qs('#draft-summary');
  summary.replaceChildren(make('strong', '', name), make('span', '', state.updateProfile ? 'Versión ' + state.updateProfile.resolved.sequence + ' → ' + qs('#revision-sequence').value : 'Primera versión'), make('span', 'draft-stage', state.staged ? 'Archivos preparados' : 'Borrador sin publicar'));
}
function goPublicationStep(step) {
  if (step > 1 && !state.updateProfile?.published) {
    try { buildManifest(); } catch (error) { setStatus(qs('#wizard-status'), describeError(error), 'error'); return; }
  }
  setStatus(qs('#wizard-status'), ''); workspaceState.step = step; syncPublicationWizard();
  qs('#publication-steps').scrollIntoView({ block: 'start', behavior: 'auto' });
}
qs('#library-search').addEventListener('input', event => { workspaceState.search = event.target.value; renderLibraryProfiles(); });
qs('#wizard-back').addEventListener('click', () => goPublicationStep(Math.max(1, workspaceState.step - 1)));
qs('#wizard-next').addEventListener('click', () => goPublicationStep(Math.min(3, workspaceState.step + 1)));
qs('#publication-steps').querySelectorAll('button').forEach(button => button.addEventListener('click', () => goPublicationStep(Number(button.dataset.step))));
new MutationObserver(() => { workspaceState.step = 1; syncPublicationWizard(); }).observe(qs('#update-context-title'), { childList: true });
new MutationObserver(syncPublicationWizard).observe(nodes.stageStatus, { childList: true });
qs('[data-view="crear"]').addEventListener('input', syncPublicationWizard);
qs('#cancel-update').addEventListener('click', () => { if (!state.updateProfile) { workspaceState.step = 1; syncPublicationWizard(); } });
window.addEventListener('hashchange', () => { if (window.location.hash === '#crear' && !state.updateProfile) { workspaceState.step = 1; syncPublicationWizard(); } });
renderLibraryProfiles(); route();
