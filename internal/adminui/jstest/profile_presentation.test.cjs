'use strict';
// Reglas del editor de presentación (artwork 1920x760, logo, autor).
const test = require('node:test');
const assert = require('node:assert');
const p = require('../static/profile-presentation.js');

test('artwork cover rectangle keeps 1920x760 aspect', () => {
  assert.deepStrictEqual(p.artworkCoverRect(1920, 760), { x: 0, y: 0, w: 1920, h: 760 });
  const tall = p.artworkCoverRect(1000, 1000);
  assert.strictEqual(tall.w, 1000);
  assert.ok(Math.abs(tall.w / tall.h - 1920 / 760) < 0.01);
  assert.strictEqual(tall.y, Math.round((1000 - tall.h) / 2));
  const wide = p.artworkCoverRect(4000, 760);
  assert.strictEqual(wide.h, 760);
  assert.ok(Math.abs(wide.w / wide.h - 1920 / 760) < 0.01);
});

test('artwork author rules', () => {
  assert.strictEqual(p.artworkAuthorError('Autora'), '');
  assert.notStrictEqual(p.artworkAuthorError(''), '');
  assert.notStrictEqual(p.artworkAuthorError('a\nb'), '');
  assert.notStrictEqual(p.artworkAuthorError('a'.repeat(65)), '');
  assert.strictEqual(p.artworkAuthorError('a'.repeat(64)), '');
});

test('logo file rules', () => {
  assert.strictEqual(p.logoFileError('image/png', 1000, 64, 32), '');
  assert.strictEqual(p.logoFileError('image/png', 1000, 1600, 800), '');
  assert.notStrictEqual(p.logoFileError('image/jpeg', 1000, 200, 80), '');
  assert.notStrictEqual(p.logoFileError('image/png', 3 * 1024 * 1024, 200, 80), '');
  for (const [w, h] of [[63, 80], [200, 31], [1601, 80], [200, 801]]) assert.notStrictEqual(p.logoFileError('image/png', 1000, w, h), '');
});

test('inherited label', () => {
  const profile = { inherited_from: { icon: 'profile_example', logo: null } };
  assert.strictEqual(p.inheritedLabel(profile, 'icon', id => 'Madre'), 'Heredado de Madre');
  assert.strictEqual(p.inheritedLabel(profile, 'logo'), '');
  assert.strictEqual(p.inheritedLabel({}, 'icon'), '');
});
