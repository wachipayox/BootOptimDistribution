package releasesigning

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDurableIdentityAndRegeneration(t *testing.T) {
	root := t.TempDir()
	first, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.KeyID() != again.KeyID() || !bytes.Equal(first.PublicKey(), again.PublicKey()) {
		t.Fatal("identity changed across restart")
	}
	if err := os.Remove(filepath.Join(root, "signing", "server-release-key.json")); err != nil {
		t.Fatal(err)
	}
	replacement, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.KeyID() == first.KeyID() {
		t.Fatal("regeneration reused lost private key")
	}
	if !bytes.Equal(replacement.PublicKeys()[first.KeyID()], first.PublicKey()) {
		t.Fatal("historical public key lost")
	}
}

func TestCorruptIdentityFailsWithoutReplacement(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "signing", "server-release-key.json")
	malformed := []byte(`{"private_key_base64url":"broken"}`)
	if err := os.WriteFile(path, malformed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil {
		t.Fatal("corrupt identity accepted")
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, malformed) {
		t.Fatal("corrupt identity was replaced")
	}
}

func TestMalformedRequestsRejected(t *testing.T) {
	signer, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `null`, `{} {}`, `{"unexpected":1}`} {
		if _, err := signer.SignRequest([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
