'use strict';
// Vista previa de noticias del panel: node --test internal/adminui/jstest/
const test = require('node:test');
const assert = require('node:assert');
const news = require('../static/news.js');

test('safe URLs: only https, and cas for images', () => {
  assert.strictEqual(news.newsSafeURL('https://example.com/a', false), 'https://example.com/a');
  assert.strictEqual(news.newsSafeURL('http://example.com/a', false), '');
  assert.strictEqual(news.newsSafeURL('javascript:alert(1)', false), '');
  assert.strictEqual(news.newsSafeURL('/ruta', true), '');
  assert.strictEqual(news.newsSafeURL('cas:' + 'a'.repeat(64), false), '');
  assert.strictEqual(news.newsSafeURL('cas:' + 'a'.repeat(64), true), '/v1/objects/sha256/' + 'a'.repeat(64));
  assert.strictEqual(news.newsSafeURL('https://example.com/' + 'a'.repeat(2048), false), '');
});

test('youtube ids', () => {
  const id = 'dQw4w9WgXcQ';
  for (const url of ['https://www.youtube.com/watch?v=' + id, 'https://www.youtube.com/watch?feature=x&v=' + id + '&t=3', 'https://youtu.be/' + id, 'https://www.youtube.com/shorts/' + id]) {
    assert.strictEqual(news.newsYoutubeID(url), id, url);
  }
  for (const url of ['http://youtu.be/' + id, 'https://youtu.be/dQw4w9WgXc', 'https://evil.example/youtu.be/' + id, 'https://youtu.be/' + id + 'x']) {
    assert.strictEqual(news.newsYoutubeID(url), '', url);
  }
});

test('blocks follow the contract shape', () => {
  const blocks = news.parseNewsBlocks('## Titulo\n\nTexto **negrita** y *cursiva*\n\n- Uno\n  - Sub\n- Dos\n\n> cita\n\n---\n\nhttps://youtu.be/dQw4w9WgXcQ\n\n![a](https://example.com/a.png)');
  assert.deepStrictEqual(blocks.map(b => b.type), ['heading', 'paragraph', 'list', 'quote', 'rule', 'youtube', 'image']);
  assert.strictEqual(blocks[0].level, 2);
  assert.deepStrictEqual(blocks[2].items.map(i => i.level), [0, 1, 0]);
  assert.strictEqual(blocks[5].id, 'dQw4w9WgXcQ');
  assert.deepStrictEqual(blocks[1].children.map(n => n.t), ['text', 'strong', 'text', 'em']);
});

test('a youtube link inside a sentence stays a paragraph', () => {
  assert.strictEqual(news.parseNewsBlocks('Mira https://youtu.be/dQw4w9WgXcQ ya')[0].type, 'paragraph');
});

test('warnings flag what the server rejects', () => {
  assert.ok(news.newsBodyWarnings('<b>x</b>').length);
  assert.ok(news.newsBodyWarnings('```\ncodigo\n```').length);
  assert.ok(news.newsBodyWarnings('# Uno').length);
  assert.ok(news.newsBodyWarnings('[x](http://example.com)').length);
  assert.ok(news.newsBodyWarnings('a | b\n--|--').length);
  assert.deepStrictEqual(news.newsBodyWarnings('## Bien\n\nTexto [x](https://example.com)'), []);
});

test('cover crop is centered 16:9', () => {
  assert.deepStrictEqual(news.newsCoverCrop(1600, 900), { sx: 0, sy: 0, sw: 1600, sh: 900 });
  assert.deepStrictEqual(news.newsCoverCrop(2000, 900), { sx: 200, sy: 0, sw: 1600, sh: 900 });
  assert.deepStrictEqual(news.newsCoverCrop(1600, 1600), { sx: 0, sy: 350, sw: 1600, sh: 900 });
});
