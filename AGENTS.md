# Distribution service operating guide

This repository is the Linux-side distribution service for the private
Wachiland Elite modpack. Read `README.md` before changing code.

- `main` is the only deployable branch. Do not add a workflow that connects
  from GitHub into the home server; the server pulls only after an explicit
  administrator request.
- Never store a release signing private key, GitHub token, deploy private key,
  player inventory or Microsoft token in the repository, service config or
  logs.
- Preserve the Pandora PR #35 contract: immutable signed revisions, SHA-256
  CAS objects, pinned inheritance, anti-rollback, hidden inaccessible objects,
  and no Start-time local filesystem scan.
- Keep the default listener on loopback. Exposing it publicly requires an
  explicit reverse-proxy/authentication design and tests.
- Update scripts must compare/install only `origin/main`, build before replacing
  a binary, and keep a recoverable previous binary.
