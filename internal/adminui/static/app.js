'use strict';

const versionNode = document.querySelector('#build-version');
const commitNode = document.querySelector('#build-commit');
const profilesNode = document.querySelector('#profiles');
const revisionsNode = document.querySelector('#revisions');

function text(value) {
  return typeof value === 'string' && value.length > 0 ? value : 'unknown';
}

function renderProfiles(profiles) {
  if (!Array.isArray(profiles) || profiles.length === 0) {
    return;
  }

  const list = document.createElement('div');
  list.className = 'profile-list';
  for (const profile of profiles) {
    const card = document.createElement('article');
    card.className = 'profile-card';

    const heading = document.createElement('h3');
    heading.textContent = text(profile.display_name);
    card.appendChild(heading);

    const meta = document.createElement('p');
    meta.className = 'meta';
    meta.textContent = `${text(profile.id)} · ${profile.official ? 'oficial' : 'no oficial'}`;
    card.appendChild(meta);
    list.appendChild(card);
  }

  profilesNode.replaceChildren(list);
  profilesNode.className = '';
  profilesNode.removeAttribute('role');
}

function renderRevisions(revisions) {
  if (!Array.isArray(revisions) || revisions.length === 0) return;
  const list = document.createElement('div');
  list.className = 'profile-list';
  for (const revision of revisions) {
    const card = document.createElement('article');
    card.className = 'profile-card';
    const heading = document.createElement('h3');
    heading.textContent = `${text(revision.profile_name)} · secuencia ${revision.sequence}`;
    card.appendChild(heading);
    const meta = document.createElement('p');
    meta.className = 'meta';
    meta.textContent = `${text(revision.id)} · ${text(revision.manifest_sha256)} · ${text(revision.published_at)}`;
    card.appendChild(meta);
    if (revision.base) {
      const base = document.createElement('p');
      base.className = 'meta';
      base.textContent = `Base fijada: ${text(revision.base.profile_id)} / ${text(revision.base.revision_id)}`;
      card.appendChild(base);
    }
    const changes = revision.changes || {};
    const delta = document.createElement('p');
    delta.className = 'meta';
    delta.textContent = `Mods +${changes.added_or_updated_mods || 0}/−${changes.removed_mods || 0} · configs +${changes.added_or_updated_configs || 0}/−${changes.removed_configs || 0} · otros ${changes.other_objects || 0}`;
    card.appendChild(delta);
    list.appendChild(card);
  }
  revisionsNode.replaceChildren(list);
  revisionsNode.className = '';
}

function formatBytes(value) {
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let amount = bytes;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit += 1; }
  return `${amount.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

fetch('/admin/api/overview', { credentials: 'same-origin', cache: 'no-store' })
  .then((response) => {
    if (!response.ok) throw new Error(`overview HTTP ${response.status}`);
    return response.json();
  })
  .then((payload) => {
    versionNode.textContent = text(payload?.build?.version);
    commitNode.textContent = text(payload?.build?.commit);
    renderProfiles(payload?.overview?.profiles);
    renderRevisions(payload?.overview?.recent_revisions);
    document.querySelector('#revision-count').textContent = String(payload?.overview?.storage?.revision_count ?? 0);
    document.querySelector('#object-count').textContent = String(payload?.overview?.storage?.object_count ?? 0);
    document.querySelector('#object-bytes').textContent = formatBytes(payload?.overview?.storage?.object_bytes);
  })
  .catch(() => {
    versionNode.textContent = 'unavailable';
    commitNode.textContent = 'unavailable';
    profilesNode.querySelector('h3').textContent = 'Administrative read model unavailable';
    profilesNode.querySelector('p').textContent = 'El panel sigue en modo lectura. Revisa los logs del servicio para ver el error.';
    revisionsNode.textContent = 'No se pudo leer el historial.';
  });
