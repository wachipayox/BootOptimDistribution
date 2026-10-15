'use strict';

// Profile JVM option rules (schema_version 3). This mirrors
// internal/revision/jvm_options.go so the panel reports problems before the
// server rejects a publication; the server remains authoritative.
const JVM_OPTION_PLATFORMS = ['pc', 'android'];
const JVM_OPTION_VMS = ['hotspot', 'openj9'];
const JVM_AUTO_FACTS = ['logical_cpus', 'memory_mib'];
// Empty on purpose. Adding a key needs the same change in Go, every launcher
// and docs/jvm-options-vectors.json.
const JVM_ALLOWED_SYSTEM_PROPERTIES = [];
const JVM_DENIED_XX_FLAGS = new Set(['Flags', 'VMOptionsFile', 'IgnoreUnrecognizedVMOptions', 'EnableJVMCI', 'EnableJVMCIProduct', 'UseJVMCICompiler', 'UseJVMCINativeLibrary', 'LogVMOutput', 'ShowMessageBoxOnError', 'PauseAtStartup', 'PauseAtExit', 'ManagementServer']);
const JVM_VALUE_PLACEHOLDER = '{value}';
const JVM_INT32_MIN = -2147483648;
const JVM_INT32_MAX = 2147483647;

function jvmAllDigits(value) { return /^[0-9]{1,19}$/.test(value); }
function jvmSize(value) { return jvmAllDigits(value.replace(/[kKmMgGtT]$/, '')); }
function jvmNumber(value) { return jvmSize(value) || /^[0-9]{1,19}\.[0-9]{1,19}$/.test(value); }

// Returns '' when the rendered argument passes the launcher allowlist.
function jvmArgumentError(arg) {
  if (typeof arg !== 'string' || !arg || arg.length > 256) return 'El argumento está vacío o supera 256 bytes.';
  if (!/^[\x21-\x7e]+$/.test(arg)) return 'El argumento debe ser ASCII imprimible sin espacios.';
  if (arg.startsWith('-XX:')) {
    const body = arg.slice(4);
    let name;
    if (body.startsWith('+') || body.startsWith('-')) name = body.slice(1);
    else {
      const at = body.indexOf('=');
      if (at < 0 || !jvmNumber(body.slice(at + 1))) return 'Los valores -XX deben ser un número o un tamaño; no se admite texto.';
      name = body.slice(0, at);
    }
    if (!/^[A-Za-z][A-Za-z0-9_]{0,79}$/.test(name)) return 'El nombre del flag -XX no es válido.';
    if (JVM_DENIED_XX_FLAGS.has(name)) return '-XX:' + name + ' no está permitido.';
    return '';
  }
  if (/^-X(ss|ms|mx)/.test(arg)) return jvmSize(arg.slice(4)) ? '' : '-Xss/-Xms/-Xmx necesitan un tamaño como 512m.';
  if (arg.startsWith('-D')) {
    const body = arg.slice(2);
    const at = body.indexOf('=');
    const key = at < 0 ? body : body.slice(0, at);
    if (at < 0 || !JVM_ALLOWED_SYSTEM_PROPERTIES.includes(key)) return 'La propiedad ' + key + ' no está en la lista permitida.';
    if (/["'\\]/.test(body.slice(at + 1))) return 'El valor de la propiedad contiene comillas o barras invertidas.';
    return '';
  }
  return 'Solo se aceptan -XX:, -Xss, -Xms, -Xmx y -D de claves permitidas.';
}

function jvmFlagKey(arg) {
  if (arg.startsWith('-XX:')) {
    const body = arg.slice(4);
    if (body.startsWith('+') || body.startsWith('-')) return 'XX:' + body.slice(1);
    return 'XX:' + body.split('=')[0];
  }
  if (arg.startsWith('-D')) return 'D:' + arg.slice(2).split('=')[0];
  return arg.slice(1, 4);
}

function jvmInteger(value) {
  return typeof value === 'number' && Number.isInteger(value) && value >= JVM_INT32_MIN && value <= JVM_INT32_MAX;
}

function jvmValueText(option, value) {
  if (option.type === 'int') {
    if (!jvmInteger(value) || !jvmInteger(option.min) || !jvmInteger(option.max) || value < option.min || value > option.max) return null;
    return String(value);
  }
  if (option.type === 'enum') return typeof value === 'string' && Array.isArray(option.values) && option.values.includes(value) ? value : null;
  return null;
}

function renderJvmOption(option, value) {
  if (option.type === 'bool') return option.template;
  const text = jvmValueText(option, value);
  return text === null ? null : option.template.replace(JVM_VALUE_PLACEHOLDER, text);
}

function jvmRenderedArguments(option) {
  if (option.type === 'bool') return [option.template];
  if (option.type === 'int') return [option.min, option.max].map(bound => option.template.replace(JVM_VALUE_PLACEHOLDER, String(bound)));
  return (option.values || []).map(value => option.template.replace(JVM_VALUE_PLACEHOLDER, value));
}

function jvmOptionFlagKey(option) {
  const rendered = jvmRenderedArguments(option)[0] || option.template || '';
  return rendered.length < 4 ? rendered : jvmFlagKey(rendered);
}

function jvmDecisionError(option, value, required) {
  if (option.type === 'bool') return value === undefined ? '' : 'Las opciones bool no llevan valor.';
  if (value === undefined) return required ? 'Falta el valor.' : '';
  return jvmValueText(option, value) === null ? 'El valor no encaja en el tipo o el rango.' : '';
}

function jvmAutoErrors(option, auto) {
  const errors = [];
  if (!auto || !Array.isArray(auto.rules) || !auto.rules.length || auto.rules.length > 16) return ['«auto» necesita entre 1 y 16 reglas.'];
  auto.rules.forEach((rule, index) => {
    const facts = rule && rule.when && typeof rule.when === 'object' ? Object.keys(rule.when) : [];
    if (!facts.length) errors.push('Regla ' + (index + 1) + ': «when» debe comprobar algún dato.');
    facts.forEach(fact => {
      const bounds = rule.when[fact] || {};
      if (!JVM_AUTO_FACTS.includes(fact)) errors.push('Regla ' + (index + 1) + ': dato desconocido «' + fact + '».');
      const min = bounds.min, max = bounds.max;
      const bad = value => value !== undefined && !(Number.isInteger(value) && value >= 0 && value <= 1073741824);
      if (min === undefined && max === undefined) errors.push('Regla ' + (index + 1) + ': ' + fact + ' necesita min o max.');
      else if (bad(min) || bad(max) || (min !== undefined && max !== undefined && min > max)) errors.push('Regla ' + (index + 1) + ': rango de ' + fact + ' no válido.');
    });
    if (typeof rule.enabled !== 'boolean') errors.push('Regla ' + (index + 1) + ': «enabled» debe ser true o false.');
    const decision = jvmDecisionError(option, rule.value, rule.enabled === true);
    if (decision) errors.push('Regla ' + (index + 1) + ': ' + decision);
  });
  const otherwise = auto.otherwise;
  if (!otherwise || typeof otherwise.enabled !== 'boolean') errors.push('«otherwise» necesita «enabled».');
  else { const decision = jvmDecisionError(option, otherwise.value, otherwise.enabled); if (decision) errors.push('otherwise: ' + decision); }
  return errors;
}

// Returns human-readable problems; an empty list means the option is publishable.
function jvmOptionErrors(option) {
  const errors = [];
  if (!option || typeof option !== 'object') return ['Opción vacía.'];
  if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(option.id || '')) errors.push('El id debe cumplir ^[a-z0-9][a-z0-9-]{0,63}$.');
  if (!option.label || option.label.length > 64 || /[\r\n]/.test(option.label)) errors.push('El nombre debe tener 1–64 caracteres en una línea.');
  if (!option.help || option.help.length > 1024) errors.push('La ayuda debe tener 1–1024 caracteres.');
  const java = option.java || {};
  if (!Number.isInteger(java.min) || java.min < 8 || java.min > 1000 || (java.max !== undefined && (!Number.isInteger(java.max) || java.max < java.min))) errors.push('El rango de Java debe empezar en 8 o más y no terminar antes de empezar.');
  const vm = option.vm || [];
  if (vm.some(item => !JVM_OPTION_VMS.includes(item)) || new Set(vm).size !== vm.length) errors.push('VM no admitida o repetida.');
  if (typeof option.template === 'string' && option.template.startsWith('-XX:') && !vm.length) errors.push('Las opciones -XX deben indicar la VM.');
  const template = typeof option.template === 'string' ? option.template : '';
  const placeholders = template.split(JVM_VALUE_PLACEHOLDER).length - 1;
  if (/[{}]/.test(template.split(JVM_VALUE_PLACEHOLDER).join(''))) errors.push('La plantilla solo admite el marcador {value}.');
  const hasRange = option.min !== undefined || option.max !== undefined;
  if (option.type === 'bool') {
    if (placeholders !== 0 || hasRange || (option.values || []).length) errors.push('Una opción bool usa una plantilla fija, sin {value}, mín., máx. ni valores.');
  } else if (option.type === 'int') {
    if (placeholders !== 1 || !jvmInteger(option.min) || !jvmInteger(option.max) || (option.values || []).length) errors.push('Una opción int necesita un {value}, mínimo y máximo enteros.');
    else if (option.min > option.max) errors.push('El mínimo supera al máximo.');
  } else if (option.type === 'enum') {
    const values = option.values || [];
    if (placeholders !== 1 || hasRange || !values.length || values.length > 16) errors.push('Una opción enum necesita un {value} y 1–16 valores.');
    if (values.some(value => !/^[A-Za-z0-9._+-]{1,64}$/.test(value)) || new Set(values).size !== values.length) errors.push('Los valores del enum no son válidos o se repiten.');
  } else errors.push('El tipo debe ser bool, int o enum.');
  if (!errors.length) {
    const rendered = jvmRenderedArguments(option);
    rendered.forEach(arg => { const problem = jvmArgumentError(arg); if (problem) errors.push(arg + ': ' + problem); });
    if (new Set(rendered.map(jvmFlagKey)).size > 1) errors.push('Todos los valores deben producir el mismo flag de la JVM.');
  }
  const platforms = option.platforms && typeof option.platforms === 'object' ? Object.keys(option.platforms) : [];
  if (!platforms.length) errors.push('Elige al menos una plataforma.');
  platforms.forEach(name => {
    const platform = option.platforms[name] || {};
    const label = name === 'pc' ? 'PC' : name === 'android' ? 'Android' : name;
    if (!JVM_OPTION_PLATFORMS.includes(name)) { errors.push('Plataforma no admitida: ' + name + '.'); return; }
    if (typeof platform.default_enabled !== 'boolean') errors.push(label + ': falta «activado por defecto».');
    const decision = jvmDecisionError(option, platform.default_value, true);
    if (decision) errors.push(label + ', valor por defecto: ' + decision);
    if (platform.auto !== undefined) jvmAutoErrors(option, platform.auto).forEach(problem => errors.push(label + ', auto: ' + problem));
  });
  return errors;
}

function jvmCollisionError(options) {
  const owners = new Map();
  for (const option of options) {
    const key = jvmOptionFlagKey(option);
    for (const platform of Object.keys(option.platforms || {})) {
      const slot = platform + '\0' + key;
      const owner = owners.get(slot);
      if (owner && owner !== option.id) return 'Las opciones «' + owner + '» y «' + option.id + '» tocan ' + key + ' en ' + platform + '.';
      owners.set(slot, option.id);
    }
  }
  return '';
}

// Applies one manifest's own options to an inherited, ordered effective list.
function applyJvmManifest(inherited, manifest) {
  const out = inherited.slice();
  (Array.isArray(manifest.remove_jvm_options) ? manifest.remove_jvm_options : []).forEach(id => {
    const at = out.findIndex(option => option.id === id);
    if (at < 0) throw new Error('Se retira el parámetro JVM «' + id + '», que el perfil base no define.');
    out.splice(at, 1);
  });
  (Array.isArray(manifest.jvm_options) ? manifest.jvm_options : []).forEach(option => {
    const at = out.findIndex(item => item.id === option.id);
    if (at >= 0) out[at] = option; else out.push(option);
  });
  return out;
}

if (typeof module !== 'undefined') {
  module.exports = { JVM_ALLOWED_SYSTEM_PROPERTIES, jvmArgumentError, jvmFlagKey, jvmOptionErrors, jvmCollisionError, applyJvmManifest, renderJvmOption };
}
