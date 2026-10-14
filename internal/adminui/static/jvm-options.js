'use strict';

// Publication step 2: the profile's JVM parameters (schema_version 3). The
// launcher shows each one as a checkbox with a "?" tooltip and applies it only
// when the platform, Java version and VM match. Rules live in jvm-options-rules.js.

const JVM_PLATFORM_LABELS = { pc: 'PC', android: 'Android' };

function draftJvmEffective() {
  return applyJvmManifest(state.parentJvmOptions, { jvm_options: Array.from(state.localJvmOptions.values()), remove_jvm_options: Array.from(state.removedJvmOptions) });
}

// Called while building either manifest; schema 3 is used only when this
// revision publishes or removes options, so other profiles stay readable by older launchers.
function applyDraftJvmOptions(manifest) {
  const own = Array.from(state.localJvmOptions.values());
  const removed = Array.from(state.removedJvmOptions);
  for (const option of own) {
    const errors = jvmOptionErrors(option);
    if (errors.length) throw new Error('Parámetro JVM «' + option.id + '»: ' + errors[0]);
  }
  if (!manifest.base && removed.length) throw new Error('Un perfil sin madre no puede retirar parámetros JVM.');
  const collision = jvmCollisionError(draftJvmEffective());
  if (collision) throw new Error(collision);
  delete manifest.jvm_options;
  delete manifest.remove_jvm_options;
  if (!own.length && !removed.length) return;
  manifest.schema_version = 3;
  manifest.config_settings = manifest.config_settings || [];
  manifest.jvm_options = JSON.parse(JSON.stringify(own));
  manifest.remove_jvm_options = removed;
}

function jvmDraftSummary(manifest) {
  if (!manifest) return '';
  const own = (manifest.jvm_options || []).length;
  const removed = (manifest.remove_jvm_options || []).length;
  const inherited = state.parentJvmOptions.length;
  if (!own && !removed) return inherited ? inherited + ' heredados, sin cambios' : 'ninguno';
  return own + ' propios, ' + removed + ' retirados (esquema 3: requiere launchers compatibles)';
}

function jvmPlatformSummary(option, name) {
  const platform = option.platforms[name];
  const arg = option.type === 'bool' ? option.template : renderJvmOption(option, platform.default_value);
  const parts = [JVM_PLATFORM_LABELS[name] || name, platform.default_enabled ? 'activado' : 'desactivado', arg || '?'];
  if (platform.auto) parts.push('automático');
  return parts.join(' · ');
}

function renderJvmOptionsPanel() {
  const list = document.querySelector('#jvm-options-list');
  if (!list) return;
  list.replaceChildren();
  const inheritedIds = new Set(state.parentJvmOptions.map(option => option.id));
  let effective;
  try { effective = draftJvmEffective(); } catch (error) { effective = []; list.appendChild(make('p', 'inline-status', describeError(error))); }
  const rows = effective.map(option => ({ option: option, origin: state.localJvmOptions.has(option.id) ? (inheritedIds.has(option.id) ? 'override' : 'own') : 'inherited' }));
  state.parentJvmOptions.filter(option => state.removedJvmOptions.has(option.id)).forEach(option => rows.push({ option: option, origin: 'removed' }));
  const originLabels = { own: 'Propio de esta versión', override: 'Sustituye al heredado', inherited: 'Heredado', removed: 'Retirado en esta versión' };
  rows.forEach(row => {
    const option = row.option;
    const item = make('article', 'jvm-option-row' + (row.origin === 'removed' ? ' jvm-option-removed' : ''));
    const heading = make('div', 'jvm-option-heading');
    heading.append(make('strong', '', option.label), make('code', '', option.id), make('span', 'status-pill', originLabels[row.origin]));
    const details = make('p', 'microcopy', Object.keys(option.platforms).map(name => jvmPlatformSummary(option, name)).join('  |  ') + '  ·  Java ' + option.java.min + (option.java.max ? '–' + option.java.max : '+') + (option.vm && option.vm.length ? ' · ' + option.vm.join(', ') : ''));
    const help = make('p', 'jvm-option-help', option.help);
    const errors = row.origin === 'removed' ? [] : jvmOptionErrors(option);
    const actions = make('div', 'card-actions');
    const button = (label, handler) => { const node = make('button', 'button button-secondary', label); node.type = 'button'; node.addEventListener('click', handler); actions.appendChild(node); };
    if (row.origin !== 'removed') button('Editar', () => openJvmOptionEditor(option, row.origin));
    if (row.origin === 'own') button('Quitar', () => { state.localJvmOptions.delete(option.id); jvmOptionsChanged(); });
    if (row.origin === 'override') button('Volver al heredado', () => { state.localJvmOptions.delete(option.id); jvmOptionsChanged(); });
    if (row.origin === 'inherited') button('Retirar', () => { state.removedJvmOptions.add(option.id); jvmOptionsChanged(); });
    if (row.origin === 'removed') button('Restaurar', () => { state.removedJvmOptions.delete(option.id); jvmOptionsChanged(); });
    item.append(heading, details, help);
    if (errors.length) item.appendChild(make('p', 'inline-status jvm-option-error', errors.join(' ')));
    item.appendChild(actions);
    list.appendChild(item);
  });
  if (!rows.length) list.appendChild(make('p', 'microcopy', 'Este perfil no define parámetros JVM.'));
  const warning = document.querySelector('#jvm-schema-warning');
  if (warning) warning.hidden = !(state.localJvmOptions.size || state.removedJvmOptions.size);
}

function jvmOptionsChanged() {
  invalidatePrepared();
  renderJvmOptionsPanel();
}

let jvmEditor = null;

function jvmField(label, input, hint) {
  const wrapper = make('label');
  const caption = make('span', '', label);
  if (hint) caption.appendChild(make('small', '', ' ' + hint));
  wrapper.append(caption, input);
  return wrapper;
}

function jvmInput(type, placeholder) {
  const input = make(type === 'textarea' ? 'textarea' : 'input');
  if (type !== 'textarea') input.type = type;
  if (placeholder) input.placeholder = placeholder;
  return input;
}

function jvmCheck(label) {
  const wrapper = make('label', 'check-row'); const input = make('input'); input.type = 'checkbox';
  wrapper.append(input, make('span', '', label));
  return { wrapper: wrapper, input: input };
}

function buildJvmEditor() {
  const dialog = make('dialog', 'file-editor jvm-option-editor');
  dialog.setAttribute('aria-labelledby', 'jvm-editor-title');
  const form = make('form'); form.method = 'dialog';
  const title = make('h2', '', 'Parámetro JVM'); title.id = 'jvm-editor-title';
  const fields = {
    id: jvmInput('text', 'jit-compiler-threads'), label: jvmInput('text', 'Hilos del compilador JIT'),
    type: make('select'), template: jvmInput('text', '-XX:CICompilerCount={value}'),
    min: jvmInput('number'), max: jvmInput('number'), values: jvmInput('text', 'G1GC, ZGC'),
    help: jvmInput('textarea', 'Qué hace exactamente, en una o dos frases. Se muestra en el «?» del launcher.'),
    javaMin: jvmInput('number', '21'), javaMax: jvmInput('number', 'sin límite')
  };
  ['bool', 'int', 'enum'].forEach(type => fields.type.add(new Option(type, type)));
  fields.help.rows = 3;
  const hotspot = jvmCheck('HotSpot'), openj9 = jvmCheck('OpenJ9');
  const grid = make('div', 'form-grid');
  grid.append(jvmField('Id estable', fields.id, '(no cambia entre versiones)'), jvmField('Nombre', fields.label), jvmField('Tipo', fields.type), jvmField('Plantilla', fields.template, '{value} = valor elegido'),
    jvmField('Mínimo', fields.min), jvmField('Máximo', fields.max), jvmField('Valores (enum)', fields.values, 'separados por comas'), jvmField('Java mínimo', fields.javaMin), jvmField('Java máximo', fields.javaMax));
  const vmRow = make('div', 'jvm-vm-row'); vmRow.append(make('span', 'microcopy', 'VM:'), hotspot.wrapper, openj9.wrapper);
  const platforms = {};
  const platformGrid = make('div', 'jvm-platform-grid');
  ['pc', 'android'].forEach(name => {
    const box = make('fieldset', 'jvm-platform');
    const available = jvmCheck('Disponible en ' + JVM_PLATFORM_LABELS[name]);
    const enabled = jvmCheck('Activado por defecto');
    const value = jvmInput('text', 'valor por defecto');
    const url = jvmInput('url', 'https://github.com/…');
    const summary = jvmInput('text', 'origen y resultado de la medición');
    const auto = jvmInput('textarea', '{"rules":[{"when":{"logical_cpus":{"max":4}},"enabled":true,"value":2}],"otherwise":{"enabled":false}}');
    auto.rows = 3; auto.spellcheck = false;
    const legend = make('legend'); legend.appendChild(available.wrapper);
    box.append(legend, enabled.wrapper, jvmField('Valor por defecto', value), jvmField('Evidencia (URL)', url), jvmField('Resumen de la evidencia', summary), jvmField('Automático (JSON, opcional)', auto, 'datos: logical_cpus, memory_mib'));
    platforms[name] = { box: box, available: available.input, enabled: enabled.input, value: value, url: url, summary: summary, auto: auto };
    platformGrid.appendChild(box);
  });
  const preview = make('pre', 'jvm-option-preview');
  const errors = make('ul', 'jvm-option-errors');
  const actions = make('div', 'dialog-actions');
  const cancel = make('button', 'button button-secondary', 'Cancelar'); cancel.value = 'cancel';
  const save = make('button', 'button button-primary', 'Guardar parámetro'); save.value = 'save';
  actions.append(cancel, save);
  form.append(title, make('p', 'microcopy', 'Solo se aceptan -XX: (booleano o numérico), -Xss, -Xms, -Xmx y -D de claves permitidas. Android nunca hereda los valores de PC: cada plataforma lleva sus valores y su evidencia.'),
    grid, jvmField('Ayuda (tooltip «?»)', fields.help), vmRow, platformGrid, preview, errors, actions);
  dialog.appendChild(form);
  document.body.appendChild(dialog);
  jvmEditor = { dialog: dialog, fields: fields, vm: { hotspot: hotspot.input, openj9: openj9.input }, platforms: platforms, preview: preview, errors: errors, save: save, origin: '', originalId: '' };
  form.addEventListener('input', updateJvmEditorPreview);
  form.addEventListener('change', updateJvmEditorPreview);
  save.addEventListener('click', event => {
    const problems = jvmEditorProblems();
    if (problems.length) { event.preventDefault(); updateJvmEditorPreview(); return; }
    const option = readJvmEditor();
    if (jvmEditor.originalId && jvmEditor.originalId !== option.id) state.localJvmOptions.delete(jvmEditor.originalId);
    state.localJvmOptions.set(option.id, option);
    state.removedJvmOptions.delete(option.id);
    jvmOptionsChanged();
  });
}

function jvmNumberValue(input) {
  const text = input.value.trim();
  if (!text) return undefined;
  const number = Number(text);
  return Number.isFinite(number) ? number : NaN;
}

function jvmTypedValue(type, text) {
  text = text.trim();
  if (type === 'bool' || !text) return undefined;
  if (type === 'int') { const number = Number(text); return Number.isFinite(number) ? number : NaN; }
  return text;
}

function readJvmEditor() {
  const f = jvmEditor.fields;
  const type = f.type.value;
  const option = { id: f.id.value.trim(), label: f.label.value.trim(), template: f.template.value.trim(), type: type, help: f.help.value.trim(), java: { min: jvmNumberValue(f.javaMin) }, platforms: {} };
  if (type === 'int') { option.min = jvmNumberValue(f.min); option.max = jvmNumberValue(f.max); }
  if (type === 'enum') option.values = f.values.value.split(',').map(value => value.trim()).filter(Boolean);
  const javaMax = jvmNumberValue(f.javaMax);
  if (javaMax !== undefined) option.java.max = javaMax;
  const vm = Object.keys(jvmEditor.vm).filter(name => jvmEditor.vm[name].checked);
  if (vm.length) option.vm = vm;
  Object.keys(jvmEditor.platforms).forEach(name => {
    const p = jvmEditor.platforms[name];
    if (!p.available.checked) return;
    const platform = { default_enabled: p.enabled.checked, evidence: { url: p.url.value.trim() } };
    const value = jvmTypedValue(type, p.value.value);
    if (value !== undefined) platform.default_value = value;
    if (p.summary.value.trim()) platform.evidence.summary = p.summary.value.trim();
    if (p.auto.value.trim()) {
      try { platform.auto = JSON.parse(p.auto.value); } catch (error) { platform.auto = { invalidJSON: true }; }
    }
    option.platforms[name] = platform;
  });
  return option;
}

function jvmEditorProblems() {
  const option = readJvmEditor();
  const problems = [];
  Object.keys(option.platforms).forEach(name => { if (option.platforms[name].auto && option.platforms[name].auto.invalidJSON) problems.push(JVM_PLATFORM_LABELS[name] + ': el bloque automático no es JSON válido.'); });
  problems.push(...jvmOptionErrors(option));
  const inheritedIds = new Set(state.parentJvmOptions.map(item => item.id));
  if (option.id !== jvmEditor.originalId && state.localJvmOptions.has(option.id)) problems.push('Ya hay un parámetro propio con el id «' + option.id + '».');
  if (jvmEditor.origin === 'new' && inheritedIds.has(option.id)) problems.push('El id «' + option.id + '» ya existe en el perfil base; edítalo desde su fila para sustituirlo.');
  if (!problems.length) {
    const own = new Map(state.localJvmOptions);
    if (jvmEditor.originalId && jvmEditor.originalId !== option.id) own.delete(jvmEditor.originalId);
    own.set(option.id, option);
    const removed = Array.from(state.removedJvmOptions).filter(id => id !== option.id);
    try {
      const collision = jvmCollisionError(applyJvmManifest(state.parentJvmOptions, { jvm_options: Array.from(own.values()), remove_jvm_options: removed }));
      if (collision) problems.push(collision);
    } catch (error) { problems.push(describeError(error)); }
  }
  return problems;
}

function updateJvmEditorPreview() {
  const f = jvmEditor.fields;
  const type = f.type.value;
  f.min.disabled = f.max.disabled = type !== 'int';
  f.values.disabled = type !== 'enum';
  Object.values(jvmEditor.platforms).forEach(p => {
    p.value.disabled = type === 'bool' || !p.available.checked;
    [p.enabled, p.url, p.summary, p.auto].forEach(input => { input.disabled = !p.available.checked; });
  });
  const option = readJvmEditor();
  const lines = Object.keys(option.platforms).map(name => {
    const platform = option.platforms[name];
    const arg = type === 'bool' ? option.template : renderJvmOption(option, platform.default_value);
    return JVM_PLATFORM_LABELS[name] + ': ' + (platform.default_enabled ? 'activado' : 'desactivado') + ' por defecto → ' + (arg || 'valor no válido');
  });
  jvmEditor.preview.textContent = lines.length ? lines.join('\n') : 'Elige al menos una plataforma.';
  const problems = jvmEditorProblems();
  jvmEditor.errors.replaceChildren(...problems.map(problem => make('li', '', problem)));
  jvmEditor.save.disabled = problems.length > 0;
}

function openJvmOptionEditor(option, origin) {
  if (!jvmEditor) buildJvmEditor();
  const f = jvmEditor.fields;
  const source = option || { id: '', label: '', template: '', type: 'int', help: '', java: { min: 21 }, vm: ['hotspot'], platforms: { pc: { default_enabled: false, evidence: { url: '' } } } };
  jvmEditor.origin = origin || 'new';
  jvmEditor.originalId = origin === 'own' || origin === 'override' ? source.id : '';
  f.id.value = source.id; f.id.readOnly = origin === 'inherited' || origin === 'override';
  f.label.value = source.label; f.type.value = source.type; f.template.value = source.template;
  f.min.value = source.min !== undefined ? source.min : ''; f.max.value = source.max !== undefined ? source.max : '';
  f.values.value = (source.values || []).join(', ');
  f.help.value = source.help;
  f.javaMin.value = source.java && source.java.min !== undefined ? source.java.min : '';
  f.javaMax.value = source.java && source.java.max !== undefined ? source.java.max : '';
  Object.keys(jvmEditor.vm).forEach(name => { jvmEditor.vm[name].checked = (source.vm || []).includes(name); });
  Object.keys(jvmEditor.platforms).forEach(name => {
    const p = jvmEditor.platforms[name];
    const platform = source.platforms[name];
    p.available.checked = Boolean(platform);
    p.enabled.checked = Boolean(platform && platform.default_enabled);
    p.value.value = platform && platform.default_value !== undefined ? String(platform.default_value) : '';
    p.url.value = platform && platform.evidence ? platform.evidence.url || '' : '';
    p.summary.value = platform && platform.evidence ? platform.evidence.summary || '' : '';
    p.auto.value = platform && platform.auto ? JSON.stringify(platform.auto) : '';
  });
  updateJvmEditorPreview();
  jvmEditor.dialog.showModal();
}

(function mountJvmOptionsPanel() {
  const review = document.querySelector('.publication-review');
  if (!review) return;
  const section = make('details', 'publication-jvm');
  section.open = true;
  section.appendChild(make('summary', '', 'Parámetros JVM del perfil'));
  section.appendChild(make('p', 'microcopy', 'Opciones que el launcher muestra como casillas en los ajustes de la instancia, separadas de los parámetros JVM personales del jugador (que siempre ganan). Solo se aplican si la plataforma, la versión de Java y la VM encajan.'));
  const warning = make('div', 'alert alert-warning', 'Esta versión publicará parámetros JVM (esquema 3). Los launchers que todavía no lo soportan no podrán instalar ni actualizar este perfil.');
  warning.id = 'jvm-schema-warning'; warning.hidden = true;
  const list = make('div', 'jvm-option-list'); list.id = 'jvm-options-list';
  const actions = make('div', 'card-actions');
  const add = make('button', 'button button-secondary', '＋ Parámetro JVM'); add.type = 'button';
  add.addEventListener('click', () => openJvmOptionEditor(null, 'new'));
  actions.appendChild(add);
  section.append(warning, list, actions);
  const reviewActions = review.querySelector(':scope > .card-actions');
  if (reviewActions) reviewActions.before(section); else review.appendChild(section);
  renderJvmOptionsPanel();
})();
