# Offline release signer

`bootoptim-release-signer` creates Ed25519 release keys and signs a canonical
request downloaded from the Distribution panel. Run it on an administrator PC,
not on the Linux service host.

## Create a key on an administrator PC

From a trusted checkout of this repository:

```powershell
go run ./cmd/bootoptim-release-signer keygen `
  --key-id wachiland-release-2026 `
  --private-out "<secure-local-folder>\wachiland-release-2026.private.json" `
  --public-out "<temporary-folder>\release-public-keys.json"
```

The private file is created without overwriting an existing path and with
owner-only mode on Unix. On Windows, save it inside a user-only folder and
protect backups with encryption. Keep at least one encrypted backup separate
from the workstation. The public JSON contains no private data and is the only
key material that belongs on Distribution. Do not put the private file in the
repository, browser upload, server, or launcher configuration.

Merge the public JSON entry into the server's existing
`release-public-keys.json` map; preserve old entries. Add the same public key to
the private launcher's trusted signer keys before publishing with it.

## Sign a publication request

After the panel has staged files, download its signing request. If the browser
blocks that download, use **Show request** in the panel, copy the complete JSON
into a local file, and continue with the same command. The request contains
the unsigned manifest and object digests, never a private key.

```powershell
go run ./cmd/bootoptim-release-signer sign `
  --key "<secure-local-folder>\wachiland-release-2026.private.json" `
  --request "<downloads>\profile.signing-request.json" `
  --out "<temporary-folder>\profile.signed-envelope.json"
```

The tool validates the canonical manifest digest, displays the profile name,
ID, revision, sequence and digest, and requires typing the exact profile ID
before signing. The output contains the signature and public identity, not the
private key. Upload that signed envelope back into the same panel workflow.

### Sign a request on Windows by dragging the JSON

Keep `scripts/sign-profile-request.sh` beside
`scripts/sign-profile-request.cmd` in a trusted checkout of this repository.
Drag the request JSON downloaded from the panel onto `sign-profile-request.cmd`.
A console opens and asks for the local Ed25519 private-key file on first use.
The script remembers only that file path in Git Bash's user config directory;
the private key itself stays in its secure folder. Later uses need only the
profile-ID confirmation required by the signer.

The signed envelope is created next to the original JSON with the suffix
`.signed-envelope.json`; for example,
`profile.signing-request.signed-envelope.json`. Existing output files are never
overwritten. Remove `~/.config/bootoptim-distribution/release-key-path` to
change the remembered key, or set `BOOTOPTIM_RELEASE_SIGNING_KEY` to override
it. The script requires Git for Windows (Git Bash) and Go on `PATH`. Upload the
signed file to the same panel workflow.

## Replace a lost workstation

If the old private key has an encrypted backup, restore it on the replacement
PC and continue with the same signer. If the key and all backups are gone,
generate a new signer on the replacement PC. Add its public key alongside the
old keys in the Distribution public-key map and Pandora's trusted-key list,
then restart the service after updating its key-map file. Existing profile IDs
and revisions stay intact; publish the next sequence using the new key ID.
Keep old public keys so launchers can verify old revisions.

Distribution intentionally does not generate or hold signing private keys. A
server able to mint its own trusted signer could forge releases if the service
were compromised. Recovery therefore creates a signer on the replacement PC
and explicitly trusts only its public key.
