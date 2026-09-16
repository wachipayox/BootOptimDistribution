# Roadmap

`main` remains the only branch that a Linux installation may deploy.
`agent/integration-current` is the integration branch for service work; every
feature lands there through a reviewed PR before a deliberate promotion to
`main`.

## Short term — required before the first Beta-profile test

1. Implement the signed immutable revision model from Pandora PR #35:
   SQLite metadata, filesystem CAS, revision validation, pinned inheritance,
   anti-rollback and Ed25519 verification. Private signing keys remain outside
   this repository and the service.
2. Implement read/admin HTTP endpoints with test-only local authentication
   boundaries. No player filesystem inventory, no launcher Start endpoint and
   no public listener by default.
3. Integrate the compatible Pandora client flow only after its persistent
   ownership/recovery stack is promoted; it must preserve local overlays and
   never scan the whole `.minecraft` at Start just to look for updates.
4. Add an **administrative web interface**: profile/channel overview, immutable
   revision history, inherited/overridden entries, publication/promotion and
   rollback status. It will be served only behind explicit administrator
   authentication/reverse-proxy policy; it is not a player launcher UI.
5. Add an unprivileged `systemd` unit, durable `/var/lib` state, `/etc`
   configuration, backup/restore instructions and a loopback-first deployment
   smoke test.

## First acceptance test

Create an admin-only `Wachiland Elite beta` revision with no real player data,
publish it to the local service, resolve it through Pandora, and prove that a
second unchanged launch performs only a small manifest request rather than a
full local modpack scan. Then validate menu and representative in-world
behavior before widening the channel.
