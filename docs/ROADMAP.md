# Roadmap

`main` remains the only branch that a Linux installation may deploy.
`agent/integration-current` is the integration branch for service work; every
feature lands there through a reviewed PR before a deliberate promotion to
`main`.

## Short term — required before the first Beta-profile test

1. [Implemented in the current integration candidate; still requires PR review]
   Signed immutable revision model from Pandora PR #35:
   SQLite metadata, filesystem CAS, revision validation, pinned inheritance,
   anti-rollback and Ed25519 verification. Private signing keys remain outside
   this repository and the service.
2. Implement authenticated distribution read/admin HTTP endpoints. The current
   panel reads the local SQLite inventory only; there is no object download API,
   release-token validation or player filesystem inventory, and no launcher
   Start endpoint.
3. Integrate the compatible Pandora client flow only after its persistent
   ownership/recovery stack is promoted; it must preserve local overlays and
   never scan the whole `.minecraft` at Start just to look for updates.
4. [Read-only inventory implemented in the current integration candidate]
   The **administrative web interface** shows persisted profiles/revisions,
   pinned inheritance and CAS aggregates. Direct LAN mode binds one private
   interface and restricts requests by trusted subnet CIDR; it is read-only,
   unauthenticated and HTTP-only, so use it only on a trusted LAN. Publication,
   promotion and rollback controls remain blocked until their authenticated
   signed admin API exists. It is not a player launcher UI.
5. [Unit example and loopback-first installation instructions now exist in the
   current candidate.] Run the first deployment smoke test, then add automated
   backup/restore procedures.

## First acceptance test

Create an admin-only `Wachiland Elite beta` revision with no real player data,
publish it to the local service, resolve it through Pandora, and prove that a
second unchanged launch performs only a small manifest request rather than a
full local modpack scan. Then validate menu and representative in-world
behavior before widening the channel.
