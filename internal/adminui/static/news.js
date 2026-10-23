'use strict';

// Noticias: lista, editor y categorías. El servidor valida todo; aquí solo hay
// una vista previa sencilla del Markdown restringido (sin HTML, sin librerías).
// Usa request/qs/make/setStatus/describeError/profileId/profileName/
// fetchProfileRevisions/revisionId/revisionSequence/sha256Bytes/uploadObject/
// loadProfiles/state de app.js.

const NEWS_COVER_WIDTH = 1600;
const NEWS_COVER_HEIGHT = 900;
const newsState = { items: [], categories: [], loading: false };

// ---- vista previa del Markdown (pura, comprobable con node) ----

function newsSafeURL(url, allowCAS) {
  if (typeof url !== 'string' || url.length > 2048) return '';
  if (/^https:\/\/[^\s]+$/.test(url)) return url;
  const cas = /^cas:([0-9a-f]{64})$/.exec(url);
  if (allowCAS && cas) return '/v1/objects/sha256/' + cas[1];
  return '';
}

function newsYoutubeID(text) {
  const value = String(text || '').trim();
  const id = '([A-Za-z0-9_-]{11})';
  const patterns = [
    new RegExp('^https://(?:www\\.|m\\.)?youtube\\.com/watch\\?(?:[^#\\s]*&)?v=' + id + '(?:&[^#\\s]*)?$'),
    new RegExp('^https://(?:www\\.|m\\.)?youtube\\.com/shorts/' + id + '(?:\\?[^#\\s]*)?$'),
    new RegExp('^https://youtu\\.be/' + id + '(?:\\?[^#\\s]*)?$')
  ];
  for (const pattern of patterns) {
    const match = pattern.exec(value);
    if (match) return match[1];
  }
  return '';
}

// Devuelve nodos {t:'text'|'strong'|'em'|'link'|'image'|'br', ...}.
function newsParseInline(text) {
  const out = [];
  let buffer = '';
  const flush = function () { if (buffer) { out.push({ t: 'text', v: buffer }); buffer = ''; } };
  let i = 0;
  while (i < text.length) {
    const rest = text.slice(i);
    let match;
    if (rest[0] === '\n') { flush(); out.push({ t: 'br' }); i += 1; continue; }
    if ((match = /^!\[([^\]]*)\]\(([^)\s]+)\)/.exec(rest))) {
      flush(); out.push({ t: 'image', alt: match[1], url: match[2] }); i += match[0].length; continue;
    }
    if ((match = /^\[([^\]]+)\]\(([^)\s]+)\)/.exec(rest))) {
      flush(); out.push({ t: 'link', children: newsParseInline(match[1]), url: match[2] }); i += match[0].length; continue;
    }
    if (rest.startsWith('**')) {
      const end = rest.indexOf('**', 2);
      if (end > 2) { flush(); out.push({ t: 'strong', children: newsParseInline(rest.slice(2, end)) }); i += end + 2; continue; }
    }
    if (rest[0] === '*' && rest[1] && !/\s/.test(rest[1])) {
      const end = rest.indexOf('*', 1);
      if (end > 1) { flush(); out.push({ t: 'em', children: newsParseInline(rest.slice(1, end)) }); i += end + 1; continue; }
    }
    if ((match = /^https:\/\/[^\s<>)]+/.exec(rest))) {
      flush(); out.push({ t: 'link', children: [{ t: 'text', v: match[0] }], url: match[0] }); i += match[0].length; continue;
    }
    buffer += rest[0];
    i += 1;
  }
  flush();
  return out;
}

const NEWS_LIST_LINE = /^( *)([-*]|\d+\.)\s+(.*)$/;

// Devuelve bloques {type, ...}. No valida: solo da forma a lo que se ve.
function parseNewsBlocks(source) {
  const lines = String(source || '').replace(/\r\n?/g, '\n').split('\n');
  const blocks = [];
  let i = 0;
  const startsBlock = function (line) {
    return /^ {0,3}#{1,6}\s/.test(line) || /^ {0,3}---+\s*$/.test(line) || /^ {0,3}>/.test(line) || NEWS_LIST_LINE.test(line);
  };
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i += 1; continue; }
    let match;
    if ((match = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line))) {
      blocks.push({ type: 'heading', level: match[1].length, children: newsParseInline(match[2]) });
      i += 1; continue;
    }
    if (/^ {0,3}---+\s*$/.test(line)) { blocks.push({ type: 'rule' }); i += 1; continue; }
    if (/^ {0,3}>/.test(line)) {
      const inner = [];
      while (i < lines.length && /^ {0,3}>/.test(lines[i])) { inner.push(lines[i].replace(/^ {0,3}> ?/, '')); i += 1; }
      blocks.push({ type: 'quote', blocks: parseNewsBlocks(inner.join('\n')) });
      continue;
    }
    if (NEWS_LIST_LINE.test(line)) {
      const items = [];
      while (i < lines.length && NEWS_LIST_LINE.test(lines[i])) {
        const m = NEWS_LIST_LINE.exec(lines[i]);
        items.push({ level: m[1].length >= 2 ? 1 : 0, ordered: /\d/.test(m[2]), text: m[3] });
        i += 1;
      }
      blocks.push({ type: 'list', items: items });
      continue;
    }
    const paragraph = [];
    while (i < lines.length && lines[i].trim() && (paragraph.length === 0 || !startsBlock(lines[i]))) {
      paragraph.push(lines[i].trim()); i += 1;
    }
    const text = paragraph.join('\n');
    const nodes = newsParseInline(text);
    const single = nodes.length === 1 ? nodes[0] : null;
    let video = '';
    if (paragraph.length === 1) {
      if (single && single.t === 'link') video = newsYoutubeID(single.url);
    }
    if (video) blocks.push({ type: 'youtube', id: video });
    else if (single && single.t === 'image') blocks.push({ type: 'image', alt: single.alt, url: single.url });
    else blocks.push({ type: 'paragraph', children: nodes });
  }
  return blocks;
}

// Avisos rápidos de lo que el servidor rechazará. El servidor decide.
function newsBodyWarnings(source) {
  const warnings = [];
  const lines = String(source || '').replace(/\r\n?/g, '\n').split('\n');
  lines.forEach(function (line, index) {
    const where = 'Línea ' + (index + 1) + ': ';
    if (/<\/?[A-Za-z!][^>]*>/.test(line)) warnings.push(where + 'el HTML no está permitido.');
    if (/^ {0,3}(```|~~~)/.test(line) || /`[^`]+`/.test(line)) warnings.push(where + 'el código no está permitido.');
    if (/^ {0,3}#(?!#)(\s|$)/.test(line) || /^ {0,3}#{4,}\s/.test(line)) warnings.push(where + 'solo se admiten títulos ## y ###.');
    if (/\]\(\s*(?!https:\/\/|cas:)[^)]*\)/.test(line)) warnings.push(where + 'los enlaces e imágenes deben ser https://.');
    if (/^\s*\|?\s*:?-{2,}:?\s*\|/.test(line)) warnings.push(where + 'las tablas no están permitidas.');
  });
  return warnings.slice(0, 8);
}

// Recorte centrado 16:9 de una imagen de sw x sh píxeles.
function newsCoverCrop(sw, sh) {
  const target = NEWS_COVER_WIDTH / NEWS_COVER_HEIGHT;
  if (sw / sh > target) {
    const w = Math.round(sh * target);
    return { sx: Math.round((sw - w) / 2), sy: 0, sw: w, sh: sh };
  }
  const h = Math.round(sw / target);
  return { sx: 0, sy: Math.round((sh - h) / 2), sw: sw, sh: h };
}

function newsRenderInline(nodes, parent) {
  nodes.forEach(function (node) {
    if (node.t === 'text') parent.append(document.createTextNode(node.v));
    else if (node.t === 'br') parent.append(document.createElement('br'));
    else if (node.t === 'strong' || node.t === 'em') {
      const element = make(node.t === 'strong' ? 'strong' : 'em');
      newsRenderInline(node.children, element); parent.append(element);
    } else if (node.t === 'link') {
      const href = newsSafeURL(node.url, false);
      if (!href) { newsRenderInline(node.children, parent); return; }
      const link = make('a'); link.href = href; link.target = '_blank'; link.rel = 'noopener noreferrer';
      newsRenderInline(node.children, link); parent.append(link);
    } else if (node.t === 'image') {
      parent.append(newsRenderImage(node));
    }
  });
}

function newsRenderImage(node) {
  const src = newsSafeURL(node.url, true);
  const holder = make('span', 'news-preview-image');
  if (src.startsWith('/v1/objects/')) {
    const image = make('img'); image.src = src; image.alt = node.alt;
    image.addEventListener('error', function () { holder.textContent = 'Imagen: ' + (node.alt || 'sin descripción'); });
    holder.append(image);
  } else {
    // El panel no carga imágenes externas (CSP); el launcher sí lo hará.
    holder.textContent = 'Imagen externa: ' + (node.alt || node.url);
  }
  return holder;
}

function newsRenderBlocks(blocks, parent) {
  blocks.forEach(function (block) {
    if (block.type === 'heading') {
      const element = make(block.level === 2 ? 'h4' : block.level === 3 ? 'h5' : 'p', block.level === 2 || block.level === 3 ? '' : 'news-preview-invalid');
      newsRenderInline(block.children, element); parent.append(element);
    } else if (block.type === 'rule') parent.append(make('hr'));
    else if (block.type === 'quote') {
      const quote = make('blockquote'); newsRenderBlocks(block.blocks, quote); parent.append(quote);
    } else if (block.type === 'list') {
      let root = null; let last = null; let sub = null;
      block.items.forEach(function (item) {
        const tag = item.ordered ? 'ol' : 'ul';
        const li = make('li'); newsRenderInline(newsParseInline(item.text), li);
        if (item.level === 0 || !last) {
          if (!root) { root = make(tag); parent.append(root); }
          root.append(li); last = li; sub = null;
        } else {
          if (!sub) { sub = make(tag); last.append(sub); }
          sub.append(li);
        }
      });
    } else if (block.type === 'youtube') {
      const card = make('div', 'news-preview-video');
      card.append(make('strong', '', 'Vídeo de YouTube'), make('span', '', block.id));
      parent.append(card);
    } else if (block.type === 'image') {
      const figure = make('p'); figure.append(newsRenderImage(block)); parent.append(figure);
    } else {
      const paragraph = make('p'); newsRenderInline(block.children, paragraph); parent.append(paragraph);
    }
  });
}

function renderNewsPreview(source, container) {
  container.replaceChildren();
  if (!String(source || '').trim()) { container.append(make('p', 'microcopy', 'La vista previa aparece aquí.')); return; }
  newsRenderBlocks(parseNewsBlocks(source), container);
}

// ---- datos ----

function newsCategoryName(id) {
  const found = newsState.categories.find(function (category) { return category.id === id; });
  return found ? found.name : id;
}

function newsGlobalProfiles() {
  return (state.profiles || []).filter(function (profile) { return !profileId(profile).startsWith('profile_test-'); });
}

function newsTargetName(id) {
  if (!id) return 'General';
  const found = newsGlobalProfiles().find(function (profile) { return profileId(profile) === id; });
  return found ? profileName(found) : id + ' (fuera del catálogo)';
}

function newsDateText(value) {
  if (!value) return 'Permanente';
  const date = new Date(value);
  const text = date.toLocaleString();
  return date.getTime() <= Date.now() ? 'Caducada · ' + text : text;
}

function newsErrorText(error) {
  const code = error && error.code;
  const detail = error && error.message ? error.message : '';
  if (code === 'invalid_news_body') return 'El cuerpo no es válido (' + detail + ').';
  if (code === 'invalid_image') return 'La portada no es válida: ' + detail;
  if (code === 'invalid_news') return 'Hay un campo no válido: ' + detail;
  if (code === 'news_limit_reached') return 'Ya hay 2000 noticias: borra alguna antigua.';
  if (code === 'category_in_use') return 'No se puede borrar una categoría que tiene noticias: cambia antes esas noticias de categoría.';
  if (code === 'news_not_found') return 'La noticia ya no existe.';
  return describeError(error);
}

async function loadNews() {
  if (newsState.loading) return;
  newsState.loading = true;
  const status = qs('#news-status');
  try {
    if (!(state.profiles || []).length) await loadProfiles();
    const results = await Promise.all([request('/v1/admin/news'), request('/v1/admin/news-categories')]);
    newsState.items = results[0].items || [];
    newsState.categories = results[1].categories || [];
    renderNewsList();
    setStatus(status, '');
  } catch (error) {
    setStatus(status, newsErrorText(error), 'error');
  } finally {
    newsState.loading = false;
  }
}

// ---- lista ----

function renderNewsList() {
  const body = qs('#news-rows');
  const rows = newsState.items.map(function (item) {
    const row = make('tr');
    const title = make('td'); title.append(make('strong', '', item.title), make('div', 'microcopy', 'por ' + item.author + ' · ' + new Date(item.published_at).toLocaleDateString()));
    row.append(title,
      make('td', '', newsCategoryName(item.category_id)),
      make('td', '', newsTargetName(item.target_profile_id)),
      make('td', '', item.pinned ? 'Sí' : '—'),
      make('td', '', newsDateText(item.expires_at)));
    const actions = make('td', 'news-actions');
    const edit = make('button', 'button button-secondary button-small', 'Editar'); edit.type = 'button';
    edit.addEventListener('click', function () { openNewsEditor(item); });
    const remove = make('button', 'button button-danger button-small', 'Borrar'); remove.type = 'button';
    remove.addEventListener('click', function () { confirmDeleteNews(item); });
    actions.append(edit, remove);
    row.append(actions);
    return row;
  });
  body.replaceChildren.apply(body, rows);
  qs('#news-empty').hidden = newsState.items.length !== 0;
  qs('#news-count').textContent = newsState.items.length + ' de 2000';
}

function confirmDeleteNews(item) {
  const dialog = make('dialog', 'compact-dialog');
  const form = make('form');
  const status = make('p', 'inline-status'); status.setAttribute('role', 'status');
  const cancel = make('button', 'button button-secondary', 'Cancelar'); cancel.type = 'button';
  const remove = make('button', 'button button-danger', 'Borrar noticia'); remove.type = 'submit';
  const actions = make('div', 'dialog-actions'); actions.append(cancel, remove);
  form.append(make('h2', '', 'Borrar noticia'), make('p', '', item.title),
    make('p', 'microcopy', 'Dejará de verse en los launchers, también si alguien la tenía guardada.'), status, actions);
  dialog.append(form); document.body.append(dialog);
  cancel.addEventListener('click', function () { dialog.close(); });
  dialog.addEventListener('close', function () { dialog.remove(); });
  form.addEventListener('submit', async function (event) {
    event.preventDefault();
    remove.disabled = cancel.disabled = true;
    setStatus(status, 'Borrando…');
    try {
      await request('/v1/admin/news/' + encodeURIComponent(item.id), { method: 'DELETE' });
      dialog.close();
      await loadNews();
    } catch (error) {
      setStatus(status, newsErrorText(error), 'error');
      remove.disabled = cancel.disabled = false;
    }
  });
  dialog.showModal();
}

// ---- imágenes ----

async function newsImageToBlob(file, width, height, crop) {
  if (!file || !['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) throw new Error('Elige una imagen PNG, JPEG o WebP.');
  if (file.size > 20 * 1024 * 1024) throw new Error('La imagen pesa demasiado (máximo 20 MB).');
  const bitmap = await createImageBitmap(file);
  try {
    const canvas = document.createElement('canvas');
    let source;
    if (crop) {
      canvas.width = width; canvas.height = height;
      source = newsCoverCrop(bitmap.width, bitmap.height);
    } else {
      const scale = Math.min(1, width / bitmap.width);
      canvas.width = Math.max(1, Math.round(bitmap.width * scale)); canvas.height = Math.max(1, Math.round(bitmap.height * scale));
      source = { sx: 0, sy: 0, sw: bitmap.width, sh: bitmap.height };
    }
    const context = canvas.getContext('2d');
    context.fillStyle = '#ffffff'; context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(bitmap, source.sx, source.sy, source.sw, source.sh, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise(function (resolve) { canvas.toBlob(resolve, 'image/jpeg', 0.86); });
    if (!blob) throw new Error('No se pudo convertir la imagen.');
    return { blob: blob, canvas: canvas, sha256: await sha256Bytes(await blob.arrayBuffer()) };
  } finally {
    bitmap.close();
  }
}

async function newsUploadImage(converted) {
  await uploadObject({ local: { sha256: converted.sha256, mediaType: 'image/jpeg', file: converted.blob } });
  return { sha256: converted.sha256, size: converted.blob.size, media_type: 'image/jpeg' };
}

// ---- editor ----

function newsField(text, input, help) {
  const label = make('label'); label.append(make('span', '', text), input);
  if (help) label.append(make('span', 'microcopy', help));
  return label;
}

function newsLocalInputValue(iso) {
  const date = new Date(iso);
  const pad = function (n) { return String(n).padStart(2, '0'); };
  return date.getFullYear() + '-' + pad(date.getMonth() + 1) + '-' + pad(date.getDate()) + 'T' + pad(date.getHours()) + ':' + pad(date.getMinutes());
}

function newsOption(value, text) { const option = make('option', '', text); option.value = value; return option; }

function openNewsEditor(item) {
  const editing = Boolean(item);
  const current = item || {};
  const dialog = make('dialog', 'profile-presentation-dialog news-dialog');
  const form = make('form', 'panel');
  dialog.setAttribute('aria-label', editing ? 'Editar noticia' : 'Nueva noticia');

  const title = make('input'); title.maxLength = 160; title.value = current.title || ''; title.required = true;
  const author = make('input'); author.maxLength = 64; author.required = true;
  let rememberedAuthor = '';
  try { rememberedAuthor = window.localStorage.getItem('news-author') || ''; } catch (_) { /* sin almacenamiento */ }
  author.value = current.author || rememberedAuthor;
  const category = make('select');
  newsState.categories.forEach(function (c) { category.append(newsOption(c.id, c.name)); });
  category.value = current.category_id || (newsState.categories[0] ? newsState.categories[0].id : '');
  const target = make('select');
  target.append(newsOption('', 'General (todos los perfiles)'));
  newsGlobalProfiles().forEach(function (profile) { target.append(newsOption(profileId(profile), profileName(profile) + ' y derivados')); });
  if (current.target_profile_id && !Array.from(target.options).some(function (o) { return o.value === current.target_profile_id; })) {
    target.append(newsOption(current.target_profile_id, current.target_profile_id + ' (fuera del catálogo)'));
  }
  target.value = current.target_profile_id || '';
  const pinned = make('input'); pinned.type = 'checkbox'; pinned.checked = Boolean(current.pinned);
  const pinnedLabel = make('label', 'news-check'); pinnedLabel.append(pinned, make('span', '', 'Fijada (aparece arriba mientras no caduque)'));

  const expiryMode = make('select');
  expiryMode.append(newsOption('never', 'Permanente'), newsOption('date', 'Caduca en una fecha'));
  const expiryDate = make('input'); expiryDate.type = 'datetime-local';
  if (current.expires_at) { expiryMode.value = 'date'; expiryDate.value = newsLocalInputValue(current.expires_at); }
  expiryDate.hidden = expiryMode.value !== 'date';
  expiryMode.addEventListener('change', function () { expiryDate.hidden = expiryMode.value !== 'date'; });

  // Portada
  const cover = { converted: null, keep: current.image && current.image.sha256 ? current.image : null };
  const coverMode = make('select');
  coverMode.append(newsOption('none', 'Sin portada'));
  if (cover.keep) coverMode.append(newsOption('keep', 'Mantener la portada subida'));
  coverMode.append(newsOption('upload', 'Subir una imagen'), newsOption('url', 'Imagen externa (URL https)'));
  coverMode.value = cover.keep ? 'keep' : current.image && current.image.url ? 'url' : 'none';
  const coverFile = make('input'); coverFile.type = 'file'; coverFile.accept = 'image/png,image/jpeg,image/webp';
  const coverURL = make('input'); coverURL.type = 'url'; coverURL.maxLength = 2048; coverURL.placeholder = 'https://example.com/portada.jpg';
  coverURL.value = current.image && current.image.url ? current.image.url : '';
  const coverPreview = make('canvas', 'news-cover-preview'); coverPreview.width = 320; coverPreview.height = 180;
  const coverNote = make('p', 'microcopy');
  function syncCover() {
    coverFile.hidden = coverMode.value !== 'upload';
    coverURL.hidden = coverMode.value !== 'url';
    coverPreview.hidden = !(coverMode.value === 'upload' && cover.converted);
    if (coverMode.value === 'url') coverNote.textContent = 'El panel no carga imágenes externas. El launcher la recorta a 16:9.';
    else if (coverMode.value === 'upload') coverNote.textContent = 'Se recorta al centro en 16:9 y se convierte a 1600 × 900 JPEG.';
    else if (coverMode.value === 'keep') coverNote.textContent = 'Se conserva la imagen ya subida.';
    else coverNote.textContent = '';
  }
  coverMode.addEventListener('change', syncCover);
  coverFile.addEventListener('change', async function () {
    cover.converted = null;
    if (!coverFile.files.length) { syncCover(); return; }
    try {
      cover.converted = await newsImageToBlob(coverFile.files[0], NEWS_COVER_WIDTH, NEWS_COVER_HEIGHT, true);
      const context = coverPreview.getContext('2d');
      context.drawImage(cover.converted.canvas, 0, 0, coverPreview.width, coverPreview.height);
      setStatus(status, '');
    } catch (error) { setStatus(status, describeError(error), 'error'); }
    syncCover();
  });

  // Revisión enlazada
  const linkProfile = make('select'); linkProfile.append(newsOption('', 'Ninguna'));
  newsGlobalProfiles().forEach(function (profile) { linkProfile.append(newsOption(profileId(profile), profileName(profile))); });
  const linkRevision = make('select'); linkRevision.hidden = true;
  async function loadLinkRevisions(wanted) {
    linkRevision.replaceChildren(); linkRevision.hidden = !linkProfile.value;
    if (!linkProfile.value) return;
    try {
      const revisions = await fetchProfileRevisions(linkProfile.value);
      revisions.forEach(function (revision) {
        linkRevision.append(newsOption(revisionId(revision), 'v' + revisionSequence(revision) + ' · ' + revisionId(revision)));
      });
      if (wanted) linkRevision.value = wanted;
    } catch (error) { setStatus(status, describeError(error), 'error'); }
  }
  if (current.linked_revision) {
    if (!Array.from(linkProfile.options).some(function (o) { return o.value === current.linked_revision.profile_id; })) {
      linkProfile.append(newsOption(current.linked_revision.profile_id, current.linked_revision.profile_id));
    }
    linkProfile.value = current.linked_revision.profile_id;
    loadLinkRevisions(current.linked_revision.revision_id);
  }
  linkProfile.addEventListener('change', function () { loadLinkRevisions(''); });

  // Cuerpo y vista previa
  const body = make('textarea', 'news-body'); body.rows = 14; body.value = current.body || ''; body.spellcheck = true;
  const preview = make('div', 'news-preview'); preview.setAttribute('aria-live', 'polite');
  const warnings = make('ul', 'news-warnings');
  function refreshPreview() {
    renderNewsPreview(body.value, preview);
    warnings.replaceChildren.apply(warnings, newsBodyWarnings(body.value).map(function (w) { return make('li', '', w); }));
    bodyCount.textContent = new TextEncoder().encode(body.value).length + ' de 65536 bytes';
  }
  const bodyCount = make('span', 'microcopy');
  let scheduled = false;
  body.addEventListener('input', function () {
    if (scheduled) return; scheduled = true;
    window.requestAnimationFrame(function () { scheduled = false; refreshPreview(); });
  });
  const insertImage = make('button', 'button button-secondary button-small', 'Insertar imagen…'); insertImage.type = 'button';
  const insertFile = make('input'); insertFile.type = 'file'; insertFile.accept = 'image/png,image/jpeg,image/webp'; insertFile.hidden = true;
  insertImage.addEventListener('click', function () { insertFile.click(); });
  insertFile.addEventListener('change', async function () {
    if (!insertFile.files.length) return;
    setStatus(status, 'Subiendo imagen…');
    try {
      const converted = await newsImageToBlob(insertFile.files[0], NEWS_COVER_WIDTH, 0, false);
      await newsUploadImage(converted);
      const markdown = '![Descripción](cas:' + converted.sha256 + ')';
      const at = body.selectionStart === undefined ? body.value.length : body.selectionStart;
      body.value = body.value.slice(0, at) + markdown + body.value.slice(body.selectionEnd);
      refreshPreview(); setStatus(status, 'Imagen subida e insertada; cambia la descripción.', 'success');
    } catch (error) { setStatus(status, describeError(error), 'error'); }
    insertFile.value = '';
  });

  const status = make('p', 'inline-status'); status.setAttribute('role', 'status');
  const cancel = make('button', 'button button-secondary', 'Cancelar'); cancel.type = 'button';
  const save = make('button', 'button button-primary', editing ? 'Guardar cambios' : 'Publicar noticia'); save.type = 'submit';
  const actions = make('div', 'dialog-actions'); actions.append(cancel, save);

  const grid = make('div', 'news-form-grid');
  grid.append(newsField('Título', title), newsField('Autor', author), newsField('Categoría', category), newsField('Destino', target,
    'General la ven todos; un perfil la ven ese perfil y sus derivados.'));
  const expiry = make('div', 'news-expiry'); expiry.append(expiryMode, expiryDate);
  const coverBox = make('div', 'news-cover'); coverBox.append(coverMode, coverFile, coverURL, coverPreview, coverNote);
  const link = make('div', 'news-link'); link.append(linkProfile, linkRevision);
  const bodyHead = make('div', 'news-body-head'); bodyHead.append(make('span', '', 'Cuerpo (Markdown)'), insertImage, insertFile, bodyCount);
  const editor = make('div', 'news-editor');
  const bodyBox = make('div', 'news-body-box'); bodyBox.append(bodyHead, body, warnings);
  const previewBox = make('div', 'news-preview-box'); previewBox.append(make('span', '', 'Vista previa'), preview);
  editor.append(bodyBox, previewBox);

  form.append(make('h3', '', editing ? 'Editar noticia' : 'Nueva noticia'), grid,
    newsField('Portada', coverBox), newsField('Caducidad', expiry), pinnedLabel,
    newsField('Revisión enlazada (opcional)', link, 'El launcher abre su changelog desde la noticia.'),
    editor,
    make('p', 'microcopy', 'Se admite: párrafos, ## y ### , **negrita**, *cursiva*, listas (2 niveles), > citas, ---, enlaces https, imágenes y vídeos de YouTube (un enlace solo en su párrafo). No se admite HTML, código ni tablas.'),
    status, actions);
  dialog.append(form); document.body.append(dialog);
  syncCover(); refreshPreview();

  cancel.addEventListener('click', function () { dialog.close(); });
  dialog.addEventListener('close', function () { dialog.remove(); });
  form.addEventListener('submit', async function (event) {
    event.preventDefault();
    save.disabled = cancel.disabled = true;
    setStatus(status, 'Guardando…');
    try {
      let image = null;
      if (coverMode.value === 'keep') image = cover.keep;
      else if (coverMode.value === 'url') image = { url: coverURL.value.trim() };
      else if (coverMode.value === 'upload') {
        if (!cover.converted) throw new Error('Elige la imagen de portada o cambia a «Sin portada».');
        image = await newsUploadImage(cover.converted);
      }
      let expires = null;
      if (expiryMode.value === 'date') {
        if (!expiryDate.value) throw new Error('Indica la fecha de caducidad o elige «Permanente».');
        expires = new Date(expiryDate.value).toISOString().replace(/\.\d{3}Z$/, 'Z');
      }
      const payload = {
        title: title.value.trim(), body: body.value, image: image, author: author.value.trim(),
        category_id: category.value, target_profile_id: target.value || null, pinned: pinned.checked,
        expires_at: expires,
        linked_revision: linkProfile.value && linkRevision.value ? { profile_id: linkProfile.value, revision_id: linkRevision.value } : null
      };
      await request(editing ? '/v1/admin/news/' + encodeURIComponent(item.id) : '/v1/admin/news', {
        method: editing ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload)
      });
      try { window.localStorage.setItem('news-author', payload.author); } catch (_) { /* sin almacenamiento */ }
      dialog.close();
      await loadNews();
    } catch (error) {
      setStatus(status, newsErrorText(error), 'error');
      save.disabled = cancel.disabled = false;
    }
  });
  dialog.showModal(); title.focus();
}

// ---- categorías ----

function openNewsCategories() {
  const dialog = make('dialog', 'profile-presentation-dialog');
  const form = make('form', 'panel');
  const rows = make('div', 'news-category-rows');
  const status = make('p', 'inline-status'); status.setAttribute('role', 'status');
  function addRow(category) {
    const row = make('div', 'news-category-row'); row.dataset.id = category.id || '';
    const name = make('input'); name.maxLength = 32; name.value = category.name || ''; name.required = true; name.setAttribute('aria-label', 'Nombre de la categoría');
    const order = make('input'); order.type = 'number'; order.step = '1'; order.value = category.order === undefined ? (rows.children.length + 1) * 10 : category.order; order.setAttribute('aria-label', 'Orden');
    const remove = make('button', 'button button-danger button-small', 'Quitar'); remove.type = 'button';
    remove.addEventListener('click', function () { row.remove(); });
    row.append(name, order, remove); rows.append(row);
  }
  newsState.categories.forEach(addRow);
  const add = make('button', 'button button-secondary', 'Añadir categoría'); add.type = 'button';
  add.addEventListener('click', function () { if (rows.children.length < 32) addRow({}); });
  const cancel = make('button', 'button button-secondary', 'Cancelar'); cancel.type = 'button';
  const save = make('button', 'button button-primary', 'Guardar categorías'); save.type = 'submit';
  const actions = make('div', 'dialog-actions'); actions.append(cancel, save);
  form.append(make('h3', '', 'Categorías de noticias'),
    make('p', 'microcopy', 'Hasta 32. Se ordenan por el número de orden y luego por nombre. Una categoría con noticias no se puede quitar.'),
    rows, add, status, actions);
  dialog.append(form); document.body.append(dialog);
  cancel.addEventListener('click', function () { dialog.close(); });
  dialog.addEventListener('close', function () { dialog.remove(); });
  form.addEventListener('submit', async function (event) {
    event.preventDefault();
    const list = Array.from(rows.children).map(function (row) {
      const inputs = row.querySelectorAll('input');
      const entry = { name: inputs[0].value.trim(), order: Math.trunc(Number(inputs[1].value) || 0) };
      if (row.dataset.id) entry.id = row.dataset.id;
      return entry;
    });
    save.disabled = cancel.disabled = true;
    setStatus(status, 'Guardando…');
    try {
      await request('/v1/admin/news-categories', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(list) });
      dialog.close();
      await loadNews();
    } catch (error) {
      setStatus(status, newsErrorText(error), 'error');
      save.disabled = cancel.disabled = false;
    }
  });
  dialog.showModal();
}

if (typeof document !== 'undefined' && document.getElementById('news-root')) {
  qs('#new-news').addEventListener('click', function () {
    if (!newsState.categories.length) { setStatus(qs('#news-status'), 'Crea primero al menos una categoría.', 'error'); openNewsCategories(); return; }
    openNewsEditor(null);
  });
  qs('#edit-news-categories').addEventListener('click', openNewsCategories);
  window.addEventListener('hashchange', function () { if (window.location.hash === '#noticias') loadNews(); });
  window.addEventListener('load', function () { if (window.location.hash === '#noticias') window.setTimeout(loadNews, 300); });
}

if (typeof module !== 'undefined') {
  module.exports = { newsSafeURL, newsYoutubeID, newsParseInline, parseNewsBlocks, newsBodyWarnings, newsCoverCrop };
}
