# Distribution service operating guide

Este servicio forma parte de **wbombo**, el repositorio central privado de los
proyectos de arranque/optimización de Wachiland Elite:
https://github.com/wachipayox/wbombo (en local: `wbombo/repos/BootOptimDistribution`,
el centro está en `../../`). Lee primero su `AGENTS.md`, `PROJECTS.md` y
`proyectos/distribution.md`. La coordinación de subagentes vive en
`coordination/` de wbombo.

This repository is the Linux-side distribution service for the private
Wachiland Elite modpack. Read `README.md` before changing code.

- `main` is the only deployable branch. `agent/integration-current` is the
  integration authority for service development; work branches start from it
  and target it with a PR. Service updates stay pull-based (the server installs
  `origin/main` when the administrator asks). GitHub workflows may call the
  service (e.g. the read API to build CI fixtures, or an authenticated endpoint
  with a dedicated key): the URL and any key live only in GitHub Actions
  secrets, never in the repository or logs (user decision 2026-10-14).
- Never store a release signing private key, GitHub token, deploy private key,
  player inventory or Microsoft token in the repository, service config or
  logs. Release signing is the explicit 2026-10-01 product-policy exception:
  the HTTPS service generates its identity only in its private persistent data
  directory; never expose it through HTTP, browser, repository or logs.
- Preserve the Pandora PR #35 contract: immutable signed revisions, SHA-256
  CAS objects, pinned inheritance, anti-rollback, hidden inaccessible objects,
  and no Start-time local filesystem scan.
- Keep the default listener on loopback. Exposing it publicly requires an
  explicit reverse-proxy/authentication design and tests.
- Update scripts must compare/install only `origin/main`, build before replacing
  a binary, and keep a recoverable previous binary.
