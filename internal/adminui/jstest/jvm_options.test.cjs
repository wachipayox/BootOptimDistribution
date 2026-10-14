'use strict';
// Keeps the admin panel's JVM option checks identical to the Go validator by
// running the shared vectors: node --test internal/adminui/jstest/
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const rules = require('../static/jvm-options-rules.js');

const vectors = JSON.parse(fs.readFileSync(path.join(__dirname, '../../../docs/jvm-options-vectors.json'), 'utf8'));

test('shared allowlist vectors', () => {
  assert.deepStrictEqual(rules.JVM_ALLOWED_SYSTEM_PROPERTIES, vectors.allowed_system_properties);
  for (const vector of vectors.accept) {
    assert.strictEqual(rules.jvmArgumentError(vector.arg), '', vector.arg);
    assert.strictEqual(rules.jvmFlagKey(vector.arg), vector.flag_key, vector.arg);
  }
  for (const vector of vectors.reject) assert.notStrictEqual(rules.jvmArgumentError(vector.arg), '', vector.arg + ' (' + vector.why + ')');
});

const jit = () => ({
  id: 'jit-compiler-threads', label: 'Hilos del compilador JIT', template: '-XX:CICompilerCount={value}',
  type: 'int', min: 2, max: 4, help: 'Limita los hilos del compilador JIT.', java: { min: 21 }, vm: ['hotspot'],
  platforms: { pc: { default_enabled: false, default_value: 2, evidence: { url: 'https://github.com/wachipayox/BootOptim/pull/354' } } }
});

test('option validation', () => {
  assert.deepStrictEqual(rules.jvmOptionErrors(jit()), []);
  assert.strictEqual(rules.renderJvmOption(jit(), 3), '-XX:CICompilerCount=3');
  assert.strictEqual(rules.renderJvmOption(jit(), 5), null);
  const auto = jit();
  auto.platforms.pc.auto = { rules: [{ when: { logical_cpus: { max: 4 } }, enabled: true, value: 2 }], otherwise: { enabled: false } };
  assert.deepStrictEqual(rules.jvmOptionErrors(auto), []);
  const broken = [
    o => { o.template = '-javaagent:{value}.jar'; },
    o => { o.vm = []; },
    o => { o.platforms.pc.default_value = 9; },
    o => { o.platforms.pc.evidence.url = 'http://example.com'; },
    o => { o.platforms.ios = o.platforms.pc; },
    o => { o.platforms.pc.auto = { rules: [{ when: { gpu: { min: 1 } }, enabled: true, value: 2 }], otherwise: { enabled: false } }; },
    o => { o.type = 'bool'; delete o.min; delete o.max; }
  ];
  for (const mutate of broken) { const option = jit(); mutate(option); assert.notDeepStrictEqual(rules.jvmOptionErrors(option), [], mutate.toString()); }
});

test('inheritance and collisions', () => {
  const parent = rules.applyJvmManifest([], { jvm_options: [jit()] });
  const replaced = jit(); replaced.platforms.pc.default_enabled = true;
  const child = rules.applyJvmManifest(parent, { jvm_options: [replaced] });
  assert.strictEqual(child.length, 1); assert.strictEqual(child[0].platforms.pc.default_enabled, true);
  assert.strictEqual(rules.applyJvmManifest(parent, { remove_jvm_options: ['jit-compiler-threads'] }).length, 0);
  assert.throws(() => rules.applyJvmManifest(parent, { remove_jvm_options: ['missing'] }));
  const copy = jit(); copy.id = 'other';
  assert.notStrictEqual(rules.jvmCollisionError([jit(), copy]), '');
  copy.platforms = { android: copy.platforms.pc };
  assert.strictEqual(rules.jvmCollisionError([jit(), copy]), '');
});
