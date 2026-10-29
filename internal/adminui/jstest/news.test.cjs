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

const sha = 'a'.repeat(64);

test('image title never ends up in the URL and carries size/align', () => {
  const cases = [
    ['![a](cas:' + sha + ' "size=large align=center")', 'cas:' + sha, 'large', 'center'],
    ["![a](https://example.com/a.png 'size=small align=left')", 'https://example.com/a.png', 'small', 'left'],
    ['![a](https://example.com/a.png "align=center size=full")', 'https://example.com/a.png', 'full', 'center'],
    ['![a](https://example.com/a.png (size=large))', 'https://example.com/a.png', 'large', ''],
    ['![a](<https://example.com/a.png> "size=small")', 'https://example.com/a.png', 'small', ''],
    ['![a](https://example.com/a.png "size=huge align=right")', 'https://example.com/a.png', '', ''],
    ['![a](https://example.com/a.png "size=large size=huge")', 'https://example.com/a.png', '', ''],
    ['![a](https://example.com/a.png "size=small size=large align=center align=left")', 'https://example.com/a.png', 'large', 'left'],
    ['![a](https://example.com/a.png "una descripcion")', 'https://example.com/a.png', '', ''],
    ['![a](https://example.com/a.png "")', 'https://example.com/a.png', '', ''],
    ['![a](https://example.com/a.png)', 'https://example.com/a.png', '', '']
  ];
  for (const [markdown, url, size, align] of cases) {
    const [block] = news.parseNewsBlocks(markdown);
    assert.strictEqual(block.type, 'image', markdown);
    assert.strictEqual(block.url, url, markdown);
    assert.strictEqual(block.size, size, markdown);
    assert.strictEqual(block.align, align, markdown);
  }
});

test('links accept a title and keep only the URL', () => {
  const [node] = news.newsParseInline('[texto](https://example.com/x "titulo")');
  assert.strictEqual(node.url, 'https://example.com/x');
});

test('shared vectors: image blocks expose the same size and align', () => {
  const vectors = JSON.parse(require('node:fs').readFileSync(require('node:path').join(__dirname, '../../../docs/news-markdown-vectors.json'), 'utf8'));
  let checked = 0;
  for (const c of vectors.cases) {
    if (!c.accepted || !c.name.startsWith('image title')) continue;
    const want = (c.blocks || []).filter(b => b.type === 'image');
    const got = news.parseNewsBlocks(c.markdown).filter(b => b.type === 'image');
    assert.strictEqual(got.length, want.length, c.name);
    want.forEach((b, i) => {
      assert.strictEqual(got[i].url, b.src, c.name);
      assert.strictEqual(got[i].size, b.size || '', c.name);
      assert.strictEqual(got[i].align, b.align || '', c.name);
    });
    checked++;
  }
  assert.ok(checked >= 10);
});

test('size caps: 180, 300, 480, full; default medium; no crop involved', () => {
  assert.strictEqual(news.newsImageLayout('small', '').width, 'min(180px, 100%)');
  assert.strictEqual(news.newsImageLayout('medium', '').width, 'min(300px, 100%)');
  assert.strictEqual(news.newsImageLayout('large', '').width, 'min(480px, 100%)');
  assert.strictEqual(news.newsImageLayout('full', '').width, '100%');
  assert.strictEqual(news.newsImageLayout('', '').width, 'min(300px, 100%)');
  assert.strictEqual(news.newsImageLayout('huge', 'right').width, 'min(300px, 100%)');
  assert.strictEqual(news.newsImageLayout('small', 'center').center, true);
  assert.strictEqual(news.newsImageLayout('small', 'left').center, false);
  assert.strictEqual(news.newsImageLayout('small', '').center, false);
});

test('view modes: realistic container widths, one at a time, scaled to fit', () => {
  assert.deepStrictEqual(news.NEWS_VIEW_MODES.map(m => [m.id, m.width]), [['pc', 960], ['portrait', 360], ['landscape', 740]]);
  assert.strictEqual(news.newsViewMode('landscape').width, 740);
  assert.strictEqual(news.newsViewMode('otro').id, 'pc');
  assert.strictEqual(news.newsFitScale(480, 960), 0.5);
  assert.strictEqual(news.newsFitScale(1000, 360), 1);
  assert.strictEqual(news.newsFitScale(0, 360), 1);
});

test('occurrences list only image-only paragraphs', () => {
  const body = 'Texto\n\n![a](cas:' + sha + ')\n\nOtro ![b](https://example.com/b.png) en texto\n\n> ![c](https://example.com/c.png "size=full")\n\n![d](https://example.com/d.png)\n![e](https://example.com/e.png)\n\n- ![f](https://example.com/f.png)';
  const found = news.newsImageOccurrences(body);
  assert.deepStrictEqual(found.map(o => o.alt), ['a', 'c', 'f']);
  assert.deepStrictEqual(found.map(o => o.line), [3, 7, 12]);
  assert.strictEqual(found[1].size, 'full');
});

test('rewriting a title touches only that image', () => {
  const a = '![a](cas:' + sha + ')';
  const b = '![b](https://example.com/b.png "size=small")';
  const c = "![c](https://example.com/c.png 'Foto del mapa size=large')";
  const body = 'Inicio **negrita**\n\n' + a + '\n\n' + b + '\n\n' + c + '\n\nFin';
  const pick = (text, index) => news.newsImageOccurrences(text)[index];

  let out = news.newsSetImageHints(body, pick(body, 0), { size: 'large', align: 'center' });
  assert.strictEqual(out, body.replace(a, '![a](cas:' + sha + ' "size=large align=center")'));

  out = news.newsSetImageHints(body, pick(body, 1), { size: 'full', align: 'left' });
  assert.strictEqual(out, body.replace(b, '![b](https://example.com/b.png "size=full")'));

  // back to the defaults removes the title; the rest of the text is untouched
  out = news.newsSetImageHints(body, pick(body, 1), { size: 'medium', align: 'left' });
  assert.strictEqual(out, body.replace(b, '![b](https://example.com/b.png)'));

  // an existing title keeps its other words and its quotes
  out = news.newsSetImageHints(body, pick(body, 2), { size: 'small', align: 'center' });
  assert.strictEqual(out, body.replace(c, "![c](https://example.com/c.png 'Foto del mapa size=small align=center')"));
  out = news.newsSetImageHints(body, pick(body, 2), { size: 'medium', align: 'left' });
  assert.strictEqual(out, body.replace(c, "![c](https://example.com/c.png 'Foto del mapa')"));

  // chained edits on several images keep each one independent
  let chained = body;
  chained = news.newsSetImageHints(chained, pick(chained, 0), { size: 'small', align: 'center' });
  chained = news.newsSetImageHints(chained, pick(chained, 1), { size: 'large', align: 'center' });
  const blocks = news.parseNewsBlocks(chained).filter(x => x.type === 'image');
  assert.deepStrictEqual(blocks.map(x => [x.size, x.align]), [['small', 'center'], ['large', 'center'], ['large', '']]);
  assert.deepStrictEqual(blocks.map(x => x.url), ['cas:' + sha, 'https://example.com/b.png', 'https://example.com/c.png']);
});

test('rewriting works inside quotes and lists and with CRLF', () => {
  const body = '> ![q](https://example.com/q.png)\r\n\r\n- ![l](https://example.com/l.png "size=small")';
  const first = news.newsImageOccurrences(body);
  const once = news.newsSetImageHints(body, first[0], { size: 'full', align: 'center' });
  assert.ok(once.startsWith('> ![q](https://example.com/q.png "size=full align=center")\r\n\r\n- '));
  const twice = news.newsSetImageHints(once, news.newsImageOccurrences(once)[1], { size: 'large', align: 'left' });
  assert.ok(twice.endsWith('- ![l](https://example.com/l.png "size=large")'));
});

test('inserting an image puts it in its own paragraph', () => {
  assert.strictEqual(news.newsInsertBlock('', 0, 0, 'X').value, 'X');
  assert.strictEqual(news.newsInsertBlock('ab', 1, 1, 'X').value, 'a\n\nX\n\nb');
  assert.strictEqual(news.newsInsertBlock('a\n', 2, 2, 'X').value, 'a\n\nX');
  assert.strictEqual(news.newsInsertBlock('a\n\nb', 3, 3, 'X').value, 'a\n\nX\n\nb');
  const inserted = news.newsInsertBlock('a', 1, 1, 'X');
  assert.strictEqual(inserted.caret, inserted.value.length);
});

