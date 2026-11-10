'use strict';
// Keeps the panel's profile.java_runtime checks and inheritance identical to
// the Go validator: node --test internal/adminui/jstest/
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const rules = require('../static/java-runtime.js');

const vectors = JSON.parse(fs.readFileSync(path.join(__dirname, '../../../docs/java-runtime-vectors.json'), 'utf8'));

test('shared accept/reject vectors', () => {
  for (const vector of vectors.accept) assert.deepStrictEqual(rules.javaRuntimeErrors(vector.java_runtime), [], JSON.stringify(vector.java_runtime));
  for (const vector of vectors.reject) assert.notDeepStrictEqual(rules.javaRuntimeErrors(vector.java_runtime), [], vector.why);
});

test('shared inheritance vectors', () => {
  for (const vector of vectors.resolve) {
    const got = rules.resolveJavaRuntime(vector.chain);
    assert.deepStrictEqual(got.pc, vector.pc, vector.name);
    assert.deepStrictEqual(got.android, vector.android, vector.name);
  }
});

test('labels', () => {
  assert.strictEqual(rules.describeJavaRuntimeChoice({ source: 'profile', major: 25 }), 'Java 25 recomendada');
  assert.strictEqual(rules.describeJavaRuntimeChoice({ source: 'mojang' }), 'Java de Mojang');
});
