'use strict';

const versionNode = document.querySelector('#build-version');
const commitNode = document.querySelector('#build-commit');
const profilesNode = document.querySelector('#profiles');

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
    meta.textContent = `${text(profile.id)} · ${text(profile.visibility)} · ${Array.isArray(profile.channels) ? profile.channels.length : 0} channel(s)`;
    card.appendChild(meta);
    list.appendChild(card);
  }

  profilesNode.replaceChildren(list);
  profilesNode.className = '';
  profilesNode.removeAttribute('role');
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
  })
  .catch(() => {
    versionNode.textContent = 'unavailable';
    commitNode.textContent = 'unavailable';
    profilesNode.querySelector('h3').textContent = 'Administrative read model unavailable';
    profilesNode.querySelector('p').textContent = 'The shell remains read-only. Check the local service logs for the underlying error.';
  });
