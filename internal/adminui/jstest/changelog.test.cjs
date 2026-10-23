'use strict';
// node --test internal/adminui/jstest/
const test = require('node:test');
const assert = require('node:assert');
const c = require('../static/changelog.js');

test('changes text round trip ignores blank lines', () => {
  assert.deepStrictEqual(c.changelogChangesFromText(' uno \r\n\r\ndos\n  \n'), ['uno', 'dos']);
  assert.strictEqual(c.changelogChangesToText(['a', 'b']), 'a\nb');
  assert.strictEqual(c.changelogChangesToText(null), '');
});

test('mod lines and origin text', () => {
  const lines = c.changelogModLines({
    added: [{ id: 'a', file: 'a.jar' }], removed: [{ id: 'b', file: 'b.jar' }], updated: [{ id: 'c', from: 'c1.jar', to: 'c2.jar' }]
  });
  assert.strictEqual(lines.length, 3);
  assert.deepStrictEqual(c.changelogModLines(null), []);
  assert.match(c.changelogOriginText({ origin: { profile_id: 'p', sequence: 4 } }, 'q'), /desde p/);
  assert.match(c.changelogOriginText({ origin: { profile_id: 'q', sequence: 4 } }, 'q'), /este perfil/);
});
