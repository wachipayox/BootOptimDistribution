'use strict';

// Publication step 1: which Java the launcher uses for this profile
// (profile.java_runtime, PROFILE_PROTOCOL.md «Java del perfil»). It lives in the
// signed "profile" object and needs no new schema, so launchers that do not
// know it (Android 1.0.0) keep using Mojang's Java. Mirrors
// internal/revision/java_runtime.go; shared cases in docs/java-runtime-vectors.json.

const JAVA_RUNTIME_PLATFORMS = ['pc', 'android'];
const JAVA_RUNTIME_PLATFORM_LABELS = { pc: 'PC', android: 'Android' };
const JAVA_RUNTIME_MIN_MAJOR = 8;
const JAVA_RUNTIME_MAX_MAJOR = 99;

function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

// Returns the problems of a profile.java_runtime value ([] when valid).
function javaRuntimeErrors(value) {
  if (!isPlainObject(value)) return ['java_runtime debe ser un objeto.'];
  const keys = Object.keys(value);
  if (!keys.length) return ['java_runtime vacío: omítelo para heredar.'];
  const errors = [];
  keys.forEach(platform => {
    if (!JAVA_RUNTIME_PLATFORMS.includes(platform)) { errors.push('Plataforma desconocida: ' + platform + '.'); return; }
    const choice = value[platform];
    const label = JAVA_RUNTIME_PLATFORM_LABELS[platform];
    if (!isPlainObject(choice)) { errors.push(label + ': la elección debe ser un objeto.'); return; }
    Object.keys(choice).forEach(key => { if (key !== 'source' && key !== 'major') errors.push(label + ': campo desconocido ' + key + '.'); });
    if (choice.source === 'mojang') {
      if ('major' in choice) errors.push(label + ': la Java de Mojang no lleva versión.');
    } else if (choice.source === 'profile') {
      if (!Number.isInteger(choice.major) || choice.major < JAVA_RUNTIME_MIN_MAJOR || choice.major > JAVA_RUNTIME_MAX_MAJOR) {
        errors.push(label + ': la versión recomendada debe ser un entero entre ' + JAVA_RUNTIME_MIN_MAJOR + ' y ' + JAVA_RUNTIME_MAX_MAJOR + '.');
      }
    } else errors.push(label + ': el origen debe ser «mojang» o «profile».');
  });
  return errors;
}

// chain: [{profile_id, java_runtime}] from the target (first) to the root (last).
function resolveJavaRuntime(chain) {
  const out = {};
  JAVA_RUNTIME_PLATFORMS.forEach(platform => {
    out[platform] = { source: 'mojang' };
    for (const item of chain) {
      const choice = item.java_runtime && item.java_runtime[platform];
      if (choice) {
        out[platform] = { source: choice.source };
        if (choice.source === 'profile') out[platform].major = choice.major;
        out[platform].from_profile_id = item.profile_id;
        break;
      }
    }
  });
  return out;
}

function describeJavaRuntimeChoice(choice) {
  return choice.source === 'profile' ? 'Java ' + choice.major + ' recomendada' : 'Java de Mojang';
}

if (typeof module !== 'undefined') {
  module.exports = { JAVA_RUNTIME_PLATFORMS, javaRuntimeErrors, resolveJavaRuntime, describeJavaRuntimeChoice };
}

if (typeof document !== 'undefined') {
  // state.javaRuntimeOwn[platform]: null (omit: inherit, or Mojang for a root
  // profile), {source:'mojang'} or {source:'profile', major}.
  // state.parentJavaRuntime: effective value of the selected mother, or null.
  state.javaRuntimeOwn = { pc: null, android: null };
  state.parentJavaRuntime = null;
  state.javaRuntimeIsChild = false;
}

// Called while building either manifest. A root profile that keeps Mojang
// omits the field, so its bytes are identical to manifests published before it.
function applyDraftJavaRuntime(manifest) {
  const own = {};
  const isChild = Boolean(manifest.base);
  JAVA_RUNTIME_PLATFORMS.forEach(platform => {
    const choice = state.javaRuntimeOwn[platform];
    if (!choice) return;
    if (!isChild && choice.source === 'mojang') return;
    own[platform] = choice.source === 'profile' ? { source: 'profile', major: choice.major } : { source: 'mojang' };
  });
  delete manifest.profile.java_runtime;
  if (!Object.keys(own).length) return;
  const errors = javaRuntimeErrors(own);
  if (errors.length) throw new Error('Java del perfil: ' + errors[0]);
  manifest.profile.java_runtime = own;
}

// Update flow: own choice of the published revision plus its mother's effective value.
function loadJavaRuntimeDraft(manifest, parentEffective) {
  const own = manifest && manifest.profile && manifest.profile.java_runtime || {};
  state.javaRuntimeOwn = { pc: own.pc ? { ...own.pc } : null, android: own.android ? { ...own.android } : null };
  state.parentJavaRuntime = parentEffective || null;
  state.javaRuntimeIsChild = Boolean(manifest && manifest.base);
  renderJavaRuntimePanel();
}

// New-profile flow: the administrator picked (or cleared) a mother.
function setParentJavaRuntime(parentEffective) {
  state.parentJavaRuntime = parentEffective || null;
  state.javaRuntimeIsChild = Boolean(parentEffective);
  renderJavaRuntimePanel();
}

function resetJavaRuntimeDraft() { loadJavaRuntimeDraft(null, null); }

function javaRuntimeDraftSummary(manifest) {
  if (!manifest) return '';
  const parentLabel = platform => state.parentJavaRuntime ? describeJavaRuntimeChoice(state.parentJavaRuntime[platform]) : 'Java de Mojang';
  const own = manifest.profile && manifest.profile.java_runtime || {};
  return JAVA_RUNTIME_PLATFORMS.map(platform => JAVA_RUNTIME_PLATFORM_LABELS[platform] + ': ' + (own[platform] ? describeJavaRuntimeChoice(own[platform]) : parentLabel(platform) + (manifest.base ? ' (heredada)' : ''))).join(' · ');
}

function renderJavaRuntimePanel() {
  const host = document.querySelector('#java-runtime-platforms');
  if (!host) return;
  host.replaceChildren();
  const isChild = state.javaRuntimeIsChild;
  JAVA_RUNTIME_PLATFORMS.forEach(platform => {
    const row = make('div', 'java-runtime-row');
    const label = make('label');
    label.appendChild(make('span', '', JAVA_RUNTIME_PLATFORM_LABELS[platform]));
    const select = document.createElement('select');
    select.id = 'java-runtime-' + platform;
    if (isChild) {
      const inherited = state.parentJavaRuntime ? describeJavaRuntimeChoice(state.parentJavaRuntime[platform]) : 'sin resolver';
      select.appendChild(new Option('Heredar de la madre (' + inherited + ')', 'inherit'));
    }
    select.appendChild(new Option('Java por defecto de Mojang', 'mojang'));
    select.appendChild(new Option('Java recomendada por el perfil', 'profile'));
    const major = document.createElement('input');
    major.type = 'number'; major.min = String(JAVA_RUNTIME_MIN_MAJOR); major.max = String(JAVA_RUNTIME_MAX_MAJOR); major.step = '1';
    major.id = 'java-runtime-' + platform + '-major'; major.placeholder = '25';
    major.setAttribute('aria-label', 'Versión de Java recomendada para ' + JAVA_RUNTIME_PLATFORM_LABELS[platform]);
    const own = state.javaRuntimeOwn[platform];
    select.value = own ? own.source : (isChild ? 'inherit' : 'mojang');
    major.value = own && own.source === 'profile' ? String(own.major) : '';
    major.hidden = select.value !== 'profile';
    const error = make('p', 'inline-status');
    error.hidden = true;
    const update = () => {
      major.hidden = select.value !== 'profile';
      error.hidden = true;
      if (select.value === 'inherit') state.javaRuntimeOwn[platform] = null;
      else if (select.value === 'mojang') state.javaRuntimeOwn[platform] = { source: 'mojang' };
      else {
        const value = Number(major.value);
        state.javaRuntimeOwn[platform] = { source: 'profile', major: value };
        const problems = javaRuntimeErrors({ [platform]: state.javaRuntimeOwn[platform] });
        if (problems.length) { error.textContent = problems[0]; error.hidden = false; }
      }
      invalidatePrepared();
    };
    select.addEventListener('change', () => { if (select.value === 'profile' && !major.value) major.value = '25'; update(); });
    major.addEventListener('input', update);
    label.appendChild(select);
    row.append(label, major, error);
    host.appendChild(row);
  });
}

if (typeof document !== 'undefined') (function mountJavaRuntimePanel() {
  const anchor = document.querySelector('.publication-jvm') || document.querySelector('.publication-identity');
  if (!anchor) return;
  const section = make('details', 'publication-jvm publication-java-runtime');
  section.appendChild(make('summary', '', 'Java del perfil'));
  section.appendChild(make('p', 'microcopy', 'Qué Java usa el launcher. «Mojang» es la que pide la versión de Minecraft. «Recomendada» fija una versión mayor (25, 21, 17…); si no arranca, el launcher vuelve a la de Mojang y lo recuerda en esa instancia. La Java elegida por el jugador en la instancia siempre gana. Un perfil hijo hereda la de su madre salvo que elija otra. No cambia el esquema: los launchers que no lo conocen usan la de Mojang.'));
  const host = make('div', 'java-runtime-platforms');
  host.id = 'java-runtime-platforms';
  section.appendChild(host);
  anchor.after(section);
  renderJavaRuntimePanel();
})();
