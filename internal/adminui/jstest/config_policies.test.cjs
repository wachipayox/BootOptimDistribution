'use strict';
// Folder/file config policy rules of the admin panel: node --test internal/adminui/jstest/
const test = require('node:test');
const assert = require('node:assert');
const rules = require('../static/config-policies.js');

const map = (entries) => new Map(entries);

test('folder rule covers files and subfolders', () => {
  const r = map([['config/fancymenu/', 'enforced']]);
  assert.deepStrictEqual(rules.resolveConfigPolicyRule('config/fancymenu/a.json', r, 'default_once'),
    { policy: 'enforced', source: 'folder', rulePath: 'config/fancymenu/' });
  assert.strictEqual(rules.resolveConfigPolicyRule('config/fancymenu/deep/x/b.txt', r, '').policy, 'enforced');
  // Prefix must be a whole folder name.
  assert.strictEqual(rules.resolveConfigPolicyRule('config/fancymenu2/a.json', r, 'default_once').source, 'published');
});

test('most specific rule wins', () => {
  const r = map([['config/', 'default_once'], ['config/fancymenu/', 'enforced'], ['config/fancymenu/user/', 'default_once'], ['config/fancymenu/user/keep.txt', 'enforced']]);
  assert.strictEqual(rules.resolveConfigPolicyRule('config/fancymenu/a.json', r, '').policy, 'enforced');
  assert.strictEqual(rules.resolveConfigPolicyRule('config/fancymenu/user/b.txt', r, '').policy, 'default_once');
  assert.deepStrictEqual(rules.resolveConfigPolicyRule('config/fancymenu/user/keep.txt', r, '').source, 'file');
  assert.strictEqual(rules.resolveConfigPolicyRule('config/rrls.toml', r, 'enforced').policy, 'default_once');
});

test('without rules the published policy stays, new files default to default_once', () => {
  assert.deepStrictEqual(rules.resolveConfigPolicyRule('config/a.toml', new Map(), 'enforced'), { policy: 'enforced', source: 'published', rulePath: '' });
  assert.deepStrictEqual(rules.resolveConfigPolicyRule('config/a.toml', new Map(), ''), { policy: 'default_once', source: 'default', rulePath: '' });
});

test('inherited layers merge root first, the child replaces the same path', () => {
  const merged = rules.mergeConfigPolicyLayers([
    { origin: 'profile_root', rules: [{ path: 'config/fancymenu/', policy: 'enforced' }, { path: 'config/a.toml', policy: 'enforced' }] },
    { origin: 'profile_child', rules: [{ path: 'config/fancymenu/', policy: 'default_once' }, { path: 'config/bad', policy: 'user_owned' }] },
  ]);
  assert.deepStrictEqual(merged.get('config/fancymenu/'), { policy: 'default_once', origin: 'profile_child' });
  assert.deepStrictEqual(merged.get('config/a.toml'), { policy: 'enforced', origin: 'profile_root' });
  assert.strictEqual(merged.has('config/bad'), false);
  // Values from merged layers resolve like plain policies.
  assert.strictEqual(rules.resolveConfigPolicyRule('config/fancymenu/x', merged, '').policy, 'default_once');
});

test('saved rules keep folder rules and drop file rules of absent configs', () => {
  const own = map([['config/fancymenu/', 'enforced'], ['config/gone.toml', 'enforced'], ['config/rrls.toml', 'default_once']]);
  assert.deepStrictEqual(rules.prunedOwnConfigPolicyRules(own, new Set(['config/rrls.toml'])), [
    { path: 'config/fancymenu/', policy: 'enforced' }, { path: 'config/rrls.toml', policy: 'default_once' },
  ]);
});

test('folder helpers', () => {
  assert.strictEqual(rules.folderRulePath('config/fancymenu'), 'config/fancymenu/');
  assert.ok(rules.isConfigPolicyPath('config'));
  assert.ok(rules.isConfigPolicyPath('config/x'));
  assert.ok(!rules.isConfigPolicyPath('resourcepacks'));
});
