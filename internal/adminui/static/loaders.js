'use strict';

// Profile modloader (schema_version 4). NeoForge profiles keep the schema 1-3
// shape `game.neoforge` so existing launchers can still read them; Fabric and
// Forge use `game.loader = {kind, version}` and schema 4. See docs/PROFILE_PROTOCOL.md.

const LOADER_LABELS = { neoforge: 'NeoForge', fabric: 'Fabric', forge: 'Forge' };
const LOADER_HINTS = {
  neoforge: 'Versiones publicadas en NeoForged Maven.',
  fabric: 'Versiones estables del loader de Fabric; el juego debe estar soportado por Fabric.',
  forge: 'Versiones de Forge publicadas en Maven, incluidas las de Minecraft antiguo.'
};

// The loader of a manifest's game object, whichever schema spelled it.
function gameLoaderOf(game) {
  if (game && game.loader) return { kind: game.loader.kind, version: game.loader.version };
  return { kind: 'neoforge', version: (game && game.neoforge) || '' };
}

function neoforgeFamily(minecraftVersion) {
  if (!state.minecraftVersions.includes(minecraftVersion)) return '';
  const parts = minecraftVersion.split('.');
  if (parts[0] === '1') return (parts[1] || '') + '.' + (parts[2] || '0') + '.';
  return (parts[0] || '') + '.' + (parts[1] || '0') + '.';
}

// Loader versions compatible with the Minecraft version currently typed.
function allowedLoaderVersions() {
  const minecraft = qs('#minecraft-version').value;
  switch (qs('#loader-kind').value) {
    case 'fabric':
      return state.fabricGameVersions.includes(minecraft) ? state.fabricVersions : [];
    case 'forge':
      return state.minecraftVersions.includes(minecraft)
        ? state.forgeVersions.filter(function (version) { return version.startsWith(minecraft + '-'); })
        : [];
    default: {
      const prefix = neoforgeFamily(minecraft);
      return prefix ? state.neoForgeVersions.filter(function (version) { return version.startsWith(prefix); }) : [];
    }
  }
}

// Minecraft releases the chosen loader supports; with no catalog for it, all of them.
function allowedMinecraftVersions() {
  switch (qs('#loader-kind').value) {
    case 'fabric':
      return state.fabricGameVersions.length ? state.minecraftVersions.filter(function (v) { return state.fabricGameVersions.includes(v); }) : state.minecraftVersions;
    case 'forge': {
      if (!state.forgeVersions.length) return state.minecraftVersions;
      const supported = new Set(state.forgeVersions.map(function (version) { return version.split('-')[0]; }));
      return state.minecraftVersions.filter(function (v) { return supported.has(v); });
    }
    default:
      return state.minecraftVersions;
  }
}

function refreshLoaderLabels() {
  const kind = qs('#loader-kind').value;
  qs('#loader-version-label').textContent = 'Versión de ' + LOADER_LABELS[kind];
  qs('#loader-version-hint').textContent = LOADER_HINTS[kind];
}

// Writes manifest.game and raises the schema only when the loader needs it.
// Called after applyDraftJvmOptions so a schema 3 choice is already in place.
function applyDraftLoader(manifest, minecraft, kind, version) {
  if (kind === 'neoforge') {
    manifest.game = { minecraft: minecraft, neoforge: version };
    return;
  }
  manifest.game = { minecraft: minecraft, loader: { kind: kind, version: version } };
  manifest.schema_version = 4;
  manifest.config_settings = manifest.config_settings || [];
  manifest.jvm_options = manifest.jvm_options || [];
  manifest.remove_jvm_options = manifest.remove_jvm_options || [];
}
