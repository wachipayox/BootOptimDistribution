'use strict';

// Servicio → Perfiles de prueba: shared test token, quota indicator and the
// list of live test profiles. Uses request/qs/make/formatBytes from app.js.

const testApi = { loading: false };

function quotaBar(label, used, max, percent, warning, usedText, maxText) {
  const row = make('div', 'test-quota-row' + (warning ? ' is-warning' : ''));
  const head = make('div', 'test-quota-head');
  head.append(make('span', 'test-quota-label', label), make('span', 'test-quota-value', usedText + ' / ' + maxText + ' · ' + Math.round(percent) + ' %' + (warning ? ' · aviso' : '')));
  const track = make('div', 'test-quota-track');
  track.setAttribute('role', 'meter');
  track.setAttribute('aria-label', label);
  track.setAttribute('aria-valuemin', '0');
  track.setAttribute('aria-valuemax', String(max));
  track.setAttribute('aria-valuenow', String(used));
  const fill = make('div', 'test-quota-fill');
  fill.style.width = Math.max(0, Math.min(100, percent)) + '%';
  track.appendChild(fill);
  row.append(head, track);
  return row;
}

function renderTestUsage(usage) {
  const bars = qs('#test-quota-bars');
  const nodes = [
    quotaBar('Perfiles de prueba vivos', usage.profiles.used, usage.profiles.max, usage.profiles.percent, usage.profiles.warning,
      String(usage.profiles.used), String(usage.profiles.max)),
    quotaBar('Bytes subidos por la API de prueba', usage.bytes.used, usage.bytes.max, usage.bytes.percent, usage.bytes.warning,
      formatBytes(usage.bytes.used), formatBytes(usage.bytes.max))
  ];
  if (usage.disk && usage.disk.available) {
    nodes.push(quotaBar('Disco del servidor (usado)', usage.disk.total_bytes - usage.disk.free_bytes, usage.disk.total_bytes, usage.disk.used_percent, usage.disk.warning,
      formatBytes(usage.disk.total_bytes - usage.disk.free_bytes), formatBytes(usage.disk.total_bytes)));
  }
  const notes = make('p', 'microcopy',
    'Objeto máximo: ' + formatBytes(usage.object_max_bytes) +
    ' · máximo efectivo para pruebas: ' + formatBytes(usage.effective_max_bytes) +
    (usage.disk && usage.disk.available ? ' · libre: ' + formatBytes(usage.disk.free_bytes) + ' (margen ' + formatBytes(usage.disk.margin_bytes) + ')' : ' · espacio libre no disponible') +
    '. Aviso a partir del ' + usage.warn_percent + ' %.');
  nodes.push(notes);
  bars.replaceChildren.apply(bars, nodes);
}

function renderTestProfiles(profiles) {
  const body = qs('#test-profile-rows');
  const rows = profiles.map(function (profile) {
    const row = make('tr');
    const parent = profile.parent ? profile.parent.profile_id + ' · ' + profile.parent.revision_id : '—';
    const latest = profile.latest_revision ? 'v' + profile.latest_revision.sequence + ' · ' + profile.latest_revision.revision_id : '—';
    [profile.profile_id + (profile.name ? ' — ' + profile.name : ''), profile.owner, parent, profile.follow_parent ? 'Sí' : 'Fijado', latest, formatBytes(profile.own_bytes)]
      .forEach(function (text) { row.appendChild(make('td', '', text)); });
    const actions = make('td');
    const remove = make('button', 'button button-danger button-small', 'Borrar');
    remove.type = 'button';
    remove.addEventListener('click', function () { deleteTestProfile(profile.profile_id); });
    actions.appendChild(remove);
    row.appendChild(actions);
    return row;
  });
  body.replaceChildren.apply(body, rows);
  qs('#test-profile-empty').hidden = profiles.length !== 0;
}

async function loadTestApi() {
  if (testApi.loading) return;
  testApi.loading = true;
  const status = qs('#test-api-status');
  try {
    const result = await request('/v1/admin/test-api');
    const token = result.token || {};
    qs('#test-token-state').textContent = token.configured
      ? 'Configurado · generado el ' + new Date(token.created_at).toLocaleString()
      : 'Sin generar: la API de prueba responde 503 hasta que lo generes';
    qs('#rotate-test-token').textContent = token.configured ? 'Rotar token' : 'Generar token';
    renderTestUsage(result.usage);
    renderTestProfiles(result.profiles || []);
    if (result.usage.warnings && result.usage.warnings.length) setStatus(status, 'Aviso: ' + result.usage.warnings.join(' · '), 'error');
    else setStatus(status, '');
  } catch (error) {
    setStatus(status, describeError(error), 'error');
  } finally {
    testApi.loading = false;
  }
}

async function rotateTestToken() {
  const configured = qs('#rotate-test-token').textContent === 'Rotar token';
  if (configured && !window.confirm('¿Rotar el token? El actual dejará de funcionar al momento para todos los agentes.')) return;
  const status = qs('#test-api-status');
  try {
    const result = await request('/v1/admin/test-api/token', { method: 'POST' });
    qs('#test-token-value').value = result.token;
    qs('#test-token-reveal').hidden = false;
    setStatus(status, 'Token nuevo generado. Guárdalo como secreto (WBOMBO_DIST_TOKEN).', 'success');
    await loadTestApi();
  } catch (error) {
    setStatus(status, describeError(error), 'error');
  }
}

async function copyTestToken() {
  const input = qs('#test-token-value');
  try {
    await navigator.clipboard.writeText(input.value);
    setStatus(qs('#test-api-status'), 'Token copiado.', 'success');
  } catch (error) {
    input.select();
    setStatus(qs('#test-api-status'), 'Selecciónalo y cópialo a mano.', 'error');
  }
}

async function deleteTestProfile(profileId) {
  const typed = window.prompt('Para borrar el perfil de prueba escribe su id exacto. El id quedará quemado y sus revisiones seguirán legibles por id.\n\n' + profileId);
  if (typed === null) return;
  const status = qs('#test-api-status');
  if (typed.trim() !== profileId) {
    setStatus(status, 'El id no coincide; no se ha borrado nada.', 'error');
    return;
  }
  try {
    await request('/v1/admin/profiles/' + encodeURIComponent(profileId), {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ confirm_profile_id: profileId })
    });
    setStatus(status, profileId + ' borrado.', 'success');
    await loadTestApi();
  } catch (error) {
    setStatus(status, describeError(error), 'error');
  }
}

(function setupTestApiPanel() {
  qs('#rotate-test-token').addEventListener('click', rotateTestToken);
  qs('#refresh-test-api').addEventListener('click', loadTestApi);
  qs('#copy-test-token').addEventListener('click', copyTestToken);
  // Hide the plaintext again when leaving the section.
  window.addEventListener('hashchange', function () {
    if (window.location.hash !== '#servicio') {
      qs('#test-token-value').value = '';
      qs('#test-token-reveal').hidden = true;
    } else {
      loadTestApi();
    }
  });
  window.addEventListener('load', function () { window.setTimeout(loadTestApi, 300); });
})();
