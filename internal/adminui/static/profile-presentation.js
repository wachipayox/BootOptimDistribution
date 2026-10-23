'use strict';

// Editor de la presentación del perfil (icono, artwork, logo). Los cambios son
// cosméticos y no publican una revisión. El servidor valida de nuevo todo.
const ARTWORK_WIDTH = 1920;
const ARTWORK_HEIGHT = 760;
const ARTWORK_MAX_BYTES = 6 * 1024 * 1024;
const LOGO_MAX_BYTES = 16 * 1024 * 1024;

// Rectángulo de origen que cubre el lienzo 1920 x 760 sin deformar la imagen.
function artworkCoverRect(width, height) {
  const target = ARTWORK_WIDTH / ARTWORK_HEIGHT;
  if (width / height > target) {
    const w = Math.round(height * target);
    return { x: Math.round((width - w) / 2), y: 0, w: w, h: height };
  }
  const h = Math.round(width / target);
  return { x: 0, y: Math.round((height - h) / 2), w: width, h: h };
}

function artworkAuthorError(text) {
  const value = String(text || '');
  if (!value.trim()) return 'Escribe el autor del artwork.';
  if (/[\r\n]/.test(value)) return 'El autor debe ir en una sola línea.';
  if (new TextEncoder().encode(value).length > 64) return 'El autor admite como máximo 64 bytes.';
  return '';
}

function logoFileError(type, size, width, height) {
  if (type !== 'image/png') return 'El logo debe ser un PNG con fondo transparente.';
  if (size > LOGO_MAX_BYTES) return 'El logo pesa más de 16 MiB.';
  if (width < 16 || width > 8192 || height < 16 || height > 8192) return 'El logo debe medir entre 16 y 8192 píxeles por lado.';
  return '';
}

// Texto «heredado de X» para icon, artwork o logo de una entrada del catálogo admin.
function inheritedLabel(profile, field, nameOf) {
  const from = profile && profile.inherited_from && profile.inherited_from[field];
  if (!from) return '';
  return 'Heredado de ' + (nameOf ? nameOf(from) : from);
}

async function artworkFromFile(file) {
  if (!file || !['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) throw new Error('Elige una imagen PNG, JPEG o WebP.');
  if (file.size > 40 * 1024 * 1024) throw new Error('La imagen es demasiado grande.');
  const bitmap = await createImageBitmap(file);
  try {
    const canvas = document.createElement('canvas');
    canvas.width = ARTWORK_WIDTH; canvas.height = ARTWORK_HEIGHT;
    const rect = artworkCoverRect(bitmap.width, bitmap.height);
    canvas.getContext('2d').drawImage(bitmap, rect.x, rect.y, rect.w, rect.h, 0, 0, ARTWORK_WIDTH, ARTWORK_HEIGHT);
    let blob = null;
    for (const quality of [0.9, 0.8, 0.7, 0.55]) {
      blob = await new Promise(resolve => canvas.toBlob(resolve, 'image/jpeg', quality));
      if (blob && blob.size <= ARTWORK_MAX_BYTES) break;
    }
    if (!blob || blob.size > ARTWORK_MAX_BYTES) throw new Error('No se pudo reducir el artwork a menos de 6 MiB.');
    const image = new File([blob], 'profile-artwork.jpg', { type: 'image/jpeg' });
    return { file: image, sha256: await sha256Bytes(await image.arrayBuffer()), mediaType: 'image/jpeg' };
  } finally { bitmap.close(); }
}

async function logoFromFile(file) {
  if (!file) throw new Error('Elige un PNG.');
  const bitmap = await createImageBitmap(file);
  try {
    const problem = logoFileError(file.type, file.size, bitmap.width, bitmap.height);
    if (problem) throw new Error(problem);
    // Sin recomprimir: se sube el archivo tal cual.
    return { file: file, sha256: await sha256Bytes(await file.arrayBuffer()), mediaType: 'image/png' };
  } finally { bitmap.close(); }
}

async function editProfilePresentation(profile) {
  const dialog = make('dialog', 'profile-presentation-dialog');
  const content = make('div', 'panel');
  const own = profile.own_presentation || {};
  const nameOf = id => {
    const found = (state.profiles || []).find(item => profileId(item) === id);
    return found ? profileName(found) : id;
  };
  const name = make('input'); name.value = own.name || profileName(profile); name.maxLength = 96;
  const description = make('textarea'); description.value = own.description ?? profile.description ?? ''; description.maxLength = 8192; description.rows = 4;
  const android = make('input'); android.type = 'checkbox'; android.checked = !!(profile.android_compatible || own.android_compatible);
  const androidField = make('label', 'checkbox-field'); androidField.append(android, make('span', '', ' Compatible con Android'));
  const status = make('p', 'inline-status'); status.setAttribute('role', 'status');
  const save = make('button', 'button button-primary', 'Guardar cambios'); save.type = 'button';
  const close = make('button', 'button button-secondary', 'Cancelar'); close.type = 'button';
  function field(text, input) { const label = make('label'); label.append(make('span', '', text), input); return label; }
  function button(text) { const b = make('button', 'button button-secondary', text); b.type = 'button'; return b; }

  // Cada bloque guarda su cambio: undefined = sin cambios, null = quitar el propio.
  const urls = [];
  function block(title, accept, effective, ownValue, previewWidth) {
    const picker = make('input'); picker.type = 'file'; picker.accept = accept;
    const preview = make('img'); preview.width = previewWidth; preview.alt = ''; preview.hidden = true;
    const hint = make('small', 'microcopy');
    const remove = button('Quitar');
    const api = { change: undefined, picker: picker, preview: preview, hint: hint, remove: remove, effective: effective, own: ownValue };
    api.refresh = function (sha, label) {
      if (sha) { preview.src = sha.startsWith('blob:') ? sha : '/v1/objects/sha256/' + encodeURIComponent(sha); preview.hidden = false; } else preview.hidden = true;
      hint.textContent = label;
    };
    api.wrapper = make('div');
    api.wrapper.append(field(title, picker), preview, hint, remove);
    return api;
  }

  const icon = block('Icono', 'image/png,image/jpeg,image/webp', profile.icon, own.icon, 64);
  const artwork = block('Artwork (1920 × 760, se recorta al elegirlo)', 'image/png,image/jpeg,image/webp', profile.artwork && profile.artwork.image, own.artwork, 240);
  const logo = block('Logo (PNG transparente, hasta 8192 × 8192)', 'image/png', profile.logo, own.logo, 160);
  const author = make('input'); author.maxLength = 64; author.value = (own.artwork && own.artwork.author) || '';
  const authorField = field('Autor del artwork', author);

  function label(api, field) {
    const inherited = inheritedLabel(profile, field, nameOf);
    if (api.change === null) return 'Se quitará el propio; volverá a heredar el de la madre (si lo tiene).';
    if (api.change) return 'Imagen nueva seleccionada.';
    if (inherited) return inherited + '.';
    return api.effective ? 'Propio.' : 'Sin imagen.';
  }
  function showCurrent(api, field, current) {
    if (api.change === null) api.refresh('', label(api, field));
    else api.refresh(api.change ? api.change.url : current, label(api, field));
    api.remove.hidden = api.change === null || (api.change === undefined && !api.own);
  }
  const currentIconSha = icon.effective && icon.effective.sha256;
  const currentArtworkSha = artwork.effective && artwork.effective.sha256;
  const currentLogoSha = logo.effective && logo.effective.sha256;
  showCurrent(icon, 'icon', currentIconSha);
  showCurrent(artwork, 'artwork', currentArtworkSha);
  showCurrent(logo, 'logo', currentLogoSha);

  async function pick(api, field, current, convert) {
    if (!api.picker.files.length) return;
    save.disabled = true; status.textContent = '';
    try {
      const prepared = await convert(api.picker.files[0]);
      if (api.change && api.change.url) URL.revokeObjectURL(api.change.url);
      prepared.url = URL.createObjectURL(prepared.file); urls.push(prepared.url);
      api.change = prepared;
      showCurrent(api, field, current);
    } catch (error) { status.textContent = describeError(error); }
    finally { api.picker.value = ''; save.disabled = false; }
  }
  icon.picker.addEventListener('change', () => pick(icon, 'icon', currentIconSha, normalizedProfileIcon));
  artwork.picker.addEventListener('change', () => pick(artwork, 'artwork', currentArtworkSha, artworkFromFile));
  logo.picker.addEventListener('change', () => pick(logo, 'logo', currentLogoSha, logoFromFile));
  for (const [api, field, current] of [[icon, 'icon', currentIconSha], [artwork, 'artwork', currentArtworkSha], [logo, 'logo', currentLogoSha]]) {
    api.remove.addEventListener('click', () => {
      // Quitar un valor aún no guardado vuelve al estado anterior; si no, quita el propio.
      if (api.change) { URL.revokeObjectURL(api.change.url); api.change = undefined; } else api.change = null;
      showCurrent(api, field, current); status.textContent = '';
    });
  }

  content.append(make('h3', '', 'Editar perfil'), field('Nombre', name), field('Descripción', description),
    icon.wrapper, artwork.wrapper, authorField, logo.wrapper, androidField, status, save, close);
  dialog.append(content); document.body.append(dialog); dialog.showModal();
  close.addEventListener('click', () => dialog.close());
  save.addEventListener('click', async () => {
    if (!name.value.trim()) { status.textContent = 'Escribe un nombre para el perfil.'; name.focus(); return; }
    const authorProblem = artworkAuthorError(author.value);
    const hasArtwork = artwork.change ? true : (artwork.change === undefined && !!own.artwork);
    if (artwork.change && authorProblem) { status.textContent = authorProblem; author.focus(); return; }
    const authorChanged = hasArtwork && !artwork.change && own.artwork && author.value.trim() !== own.artwork.author;
    if (authorChanged && authorProblem) { status.textContent = authorProblem; author.focus(); return; }
    save.disabled = close.disabled = true; status.textContent = 'Guardando…';
    try {
      const body = { name: name.value.trim(), description: description.value.trim(), android_compatible: android.checked };
      for (const [api, key] of [[icon, 'icon'], [logo, 'logo']]) {
        if (api.change === null) body[key] = null;
        else if (api.change) {
          await uploadObject({ local: { file: api.change.file, sha256: api.change.sha256, mediaType: api.change.mediaType } });
          body[key] = { sha256: api.change.sha256, size: api.change.file.size, media_type: api.change.mediaType };
        }
      }
      if (artwork.change === null) body.artwork = null;
      else if (artwork.change) {
        await uploadObject({ local: { file: artwork.change.file, sha256: artwork.change.sha256, mediaType: artwork.change.mediaType } });
        body.artwork = { image: { sha256: artwork.change.sha256, size: artwork.change.file.size, media_type: artwork.change.mediaType }, author: author.value.trim() };
      } else if (authorChanged) {
        body.artwork = { image: own.artwork.image, author: author.value.trim() };
      }
      await request('/v1/admin/profiles/' + encodeURIComponent(profileId(profile)) + '/presentation', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      await loadProfiles(); dialog.close();
    } catch (error) { status.textContent = describeError(error); }
    finally { save.disabled = close.disabled = false; }
  });
  dialog.addEventListener('close', () => { urls.forEach(url => URL.revokeObjectURL(url)); dialog.remove(); });
}

if (typeof module !== 'undefined') {
  module.exports = { artworkCoverRect, artworkAuthorError, logoFileError, inheritedLabel };
}
