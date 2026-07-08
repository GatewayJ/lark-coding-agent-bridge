package secretstore

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type keystoreFixture struct {
	Seed        string          `json:"seed"`
	SaltBase64  string          `json:"saltBase64"`
	SecretID    string          `json:"secretId"`
	Plaintext   string          `json:"plaintext"`
	Store       json.RawMessage `json:"store"`
	decodedSalt []byte
}

func TestKeystoreDecryptsCompatFixture(t *testing.T) {
	fixture := loadKeystoreFixture(t)
	paths := writeFixtureKeystore(t, fixture)
	store, err := NewKeystore(KeystoreOptions{Paths: paths, Seed: fixture.Seed})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}

	got, ok, err := store.GetSecret(fixture.SecretID)
	if err != nil {
		t.Fatalf("GetSecret returned error: %v", err)
	}
	if !ok {
		t.Fatalf("GetSecret ok = false, want true")
	}
	if got != fixture.Plaintext {
		t.Fatalf("GetSecret = %q, want %q", got, fixture.Plaintext)
	}
}

func TestKeystoreEncryptsCompatFixture(t *testing.T) {
	fixture := loadKeystoreFixture(t)
	root := t.TempDir()
	paths := testKeystorePaths(root)
	if err := os.MkdirAll(filepath.Dir(paths.KeystoreSaltFile), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(paths.KeystoreSaltFile, fixture.decodedSalt, 0o600); err != nil {
		t.Fatalf("WriteFile salt returned error: %v", err)
	}
	iv := []byte{0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b}
	store, err := NewKeystore(KeystoreOptions{
		Paths: paths,
		Seed:  fixture.Seed,
		Rand:  bytes.NewReader(iv),
	})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	if err := store.SetSecret(fixture.SecretID, fixture.Plaintext); err != nil {
		t.Fatalf("SetSecret returned error: %v", err)
	}
	got, err := os.ReadFile(paths.SecretsFile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	assertJSONEqual(t, got, fixture.Store)
}

func TestKeystoreRejectsWrongSeedOrSalt(t *testing.T) {
	fixture := loadKeystoreFixture(t)
	paths := writeFixtureKeystore(t, fixture)
	wrongSeed, err := NewKeystore(KeystoreOptions{Paths: paths, Seed: "wrong-host|wrong-user"})
	if err != nil {
		t.Fatalf("NewKeystore wrong seed returned error: %v", err)
	}
	if _, _, err := wrongSeed.GetSecret(fixture.SecretID); err == nil {
		t.Fatalf("GetSecret with wrong seed returned nil error")
	}

	if err := os.WriteFile(paths.KeystoreSaltFile, bytes.Repeat([]byte{0xff}, 32), 0o600); err != nil {
		t.Fatalf("WriteFile wrong salt returned error: %v", err)
	}
	wrongSalt, err := NewKeystore(KeystoreOptions{Paths: paths, Seed: fixture.Seed})
	if err != nil {
		t.Fatalf("NewKeystore wrong salt returned error: %v", err)
	}
	if _, _, err := wrongSalt.GetSecret(fixture.SecretID); err == nil {
		t.Fatalf("GetSecret with wrong salt returned nil error")
	}
}

func TestKeystoreSetRemoveAndList(t *testing.T) {
	store, err := NewKeystore(KeystoreOptions{Paths: testKeystorePaths(t.TempDir()), Seed: "host|user"})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	if err := store.SetSecret("app-one", "secret-one"); err != nil {
		t.Fatalf("SetSecret returned error: %v", err)
	}
	got, ok, err := store.GetSecret("app-one")
	if err != nil || !ok || got != "secret-one" {
		t.Fatalf("GetSecret = (%q, %v, %v), want secret-one true nil", got, ok, err)
	}
	ids, err := store.ListSecretIDs()
	if err != nil {
		t.Fatalf("ListSecretIDs returned error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "app-one" {
		t.Fatalf("ListSecretIDs = %#v, want [app-one]", ids)
	}
	removed, err := store.RemoveSecret("app-one")
	if err != nil || !removed {
		t.Fatalf("RemoveSecret = (%v, %v), want true nil", removed, err)
	}
	_, ok, err = store.GetSecret("app-one")
	if err != nil || ok {
		t.Fatalf("GetSecret after remove = (%v, %v), want false nil", ok, err)
	}
}

func loadKeystoreFixture(t *testing.T) keystoreFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "compat", "secrets", "keystore-fixture.json"))
	if err != nil {
		t.Fatalf("ReadFile fixture returned error: %v", err)
	}
	var fixture keystoreFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("Unmarshal fixture returned error: %v", err)
	}
	fixture.decodedSalt, err = base64.StdEncoding.DecodeString(fixture.SaltBase64)
	if err != nil {
		t.Fatalf("DecodeString salt returned error: %v", err)
	}
	return fixture
}

func writeFixtureKeystore(t *testing.T, fixture keystoreFixture) KeystorePaths {
	t.Helper()
	paths := testKeystorePaths(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(paths.SecretsFile), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(paths.KeystoreSaltFile, fixture.decodedSalt, 0o600); err != nil {
		t.Fatalf("WriteFile salt returned error: %v", err)
	}
	if err := os.WriteFile(paths.SecretsFile, append(fixture.Store, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile store returned error: %v", err)
	}
	return paths
}

func testKeystorePaths(root string) KeystorePaths {
	return KeystorePaths{
		SecretsFile:         filepath.Join(root, "profiles", "claude", "secrets.enc"),
		KeystoreSaltFile:    filepath.Join(root, "profiles", "claude", ".keystore.salt"),
		SecretsGetterScript: filepath.Join(root, "secrets-getter"),
	}
}

func assertJSONEqual(t *testing.T, got []byte, want []byte) {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("got invalid JSON: %v\n%s", err, string(got))
	}
	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("want invalid JSON: %v\n%s", err, string(want))
	}
	if !jsonEqual(gotValue, wantValue) {
		gotPretty, _ := json.MarshalIndent(gotValue, "", "  ")
		wantPretty, _ := json.MarshalIndent(wantValue, "", "  ")
		t.Fatalf("JSON mismatch\ngot:  %s\nwant: %s", gotPretty, wantPretty)
	}
}

func jsonEqual(a any, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return bytes.Equal(aj, bj)
}
