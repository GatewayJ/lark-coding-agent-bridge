package cardauth

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSignAndVerifyBindsAllContextFields(t *testing.T) {
	auth, _ := testAuth(t, nil)
	token, err := auth.Sign(baseSignInput())
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	if !strings.HasPrefix(token, "bridge_cb.v1.") {
		t.Fatalf("token = %q, want bridge callback prefix", token)
	}
	result := auth.Verify(token, baseExpected())
	if !result.OK {
		t.Fatalf("Verify rejected token: %#v", result)
	}

	mismatch := baseExpected()
	mismatch.Action = "resume"
	result = auth.Verify(token, mismatch)
	if result.OK || result.Reason != VerifyContextMismatch {
		t.Fatalf("Verify mismatch = %#v, want context-mismatch", result)
	}
}

func TestVerifyRejectsExpiredAndReplayAcrossReload(t *testing.T) {
	now := time.UnixMilli(1000)
	path := filepath.Join(t.TempDir(), "nonces.json")
	store := NewNonceStore(path)
	auth, err := New(Options{
		Keys:        []Key{{Version: 1, Secret: "secret-1"}},
		NonceStore:  store,
		Now:         func() time.Time { return now },
		CreateNonce: func() (string, error) { return "nonce-1", nil },
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	token, err := auth.Sign(baseSignInput())
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	now = time.UnixMilli(1000 + 60_001)
	if result := auth.Verify(token, baseExpected()); result.OK || result.Reason != VerifyExpired {
		t.Fatalf("expired verify = %#v, want expired", result)
	}

	now = time.UnixMilli(1000)
	if result := auth.Verify(token, baseExpected()); !result.OK {
		t.Fatalf("Verify rejected token: %#v", result)
	}
	if result := auth.Verify(token, baseExpected()); result.OK || result.Reason != VerifyNonceReplay {
		t.Fatalf("replay verify = %#v, want nonce-replay", result)
	}
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	reloadedStore := NewNonceStore(path)
	if err := reloadedStore.Load(); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	reloaded, err := New(Options{
		Keys:        []Key{{Version: 1, Secret: "secret-1"}},
		NonceStore:  reloadedStore,
		Now:         func() time.Time { return now },
		CreateNonce: func() (string, error) { return "unused", nil },
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if result := reloaded.Verify(token, baseExpected()); result.OK || result.Reason != VerifyNonceReplay {
		t.Fatalf("reloaded replay verify = %#v, want nonce-replay", result)
	}
}

func TestRetiredKeysVerifyButNewestActiveSigns(t *testing.T) {
	auth, _ := testAuth(t, []Key{
		{Version: 1, Secret: "old-secret", Retired: true},
		{Version: 2, Secret: "new-secret"},
	})
	token, err := auth.Sign(baseSignInput())
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	result := auth.Verify(token, baseExpected())
	if !result.OK || result.Payload.KeyVersion != 2 {
		t.Fatalf("Verify = %#v, want active key version 2", result)
	}

	oldStore := NewNonceStore("")
	oldAuth, err := New(Options{
		Keys:        []Key{{Version: 1, Secret: "old-secret"}},
		NonceStore:  oldStore,
		Now:         func() time.Time { return time.UnixMilli(1000) },
		CreateNonce: func() (string, error) { return "old-nonce", nil },
	})
	if err != nil {
		t.Fatalf("New old auth returned error: %v", err)
	}
	oldToken, err := oldAuth.Sign(baseSignInput())
	if err != nil {
		t.Fatalf("old Sign returned error: %v", err)
	}
	if result := auth.Verify(oldToken, baseExpected()); !result.OK || result.Payload.KeyVersion != 1 {
		t.Fatalf("old token verify = %#v, want retired key accepted", result)
	}
}

func TestVerifyRejectsRevokedNonceAndBadSignature(t *testing.T) {
	store := NewNonceStore("")
	auth, err := New(Options{
		Keys:        []Key{{Version: 1, Secret: "secret-1"}},
		NonceStore:  store,
		Now:         func() time.Time { return time.UnixMilli(1000) },
		CreateNonce: func() (string, error) { return "nonce-revoke", nil },
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	token, err := auth.Sign(baseSignInput())
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	store.Revoke("nonce-revoke")
	if result := auth.Verify(token, baseExpected()); result.OK || result.Reason != VerifyNonceRevoked {
		t.Fatalf("revoked verify = %#v, want nonce-revoked", result)
	}

	tampered := token[:len(token)-1] + "x"
	if result := auth.Verify(tampered, baseExpected()); result.OK || result.Reason != VerifyBadSignature {
		t.Fatalf("tampered verify = %#v, want bad-signature", result)
	}
}

func testAuth(t *testing.T, keys []Key) (*Auth, *NonceStore) {
	t.Helper()
	if keys == nil {
		keys = []Key{{Version: 1, Secret: "secret-1"}}
	}
	store := NewNonceStore("")
	auth, err := New(Options{
		Keys:        keys,
		NonceStore:  store,
		Now:         func() time.Time { return time.UnixMilli(1000) },
		CreateNonce: func() (string, error) { return "nonce-1", nil },
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return auth, store
}

func baseSignInput() SignInput {
	return SignInput{
		RunID:             "run-1",
		Scope:             "chat-1",
		ChatID:            "oc_1",
		OperatorOpenID:    "ou_1",
		Action:            "stop",
		PolicyFingerprint: "fp-1",
		TTL:               60 * time.Second,
	}
}

func baseExpected() VerifyExpected {
	return VerifyExpected{
		RunID:             "run-1",
		Scope:             "chat-1",
		ChatID:            "oc_1",
		OperatorOpenID:    "ou_1",
		Action:            "stop",
		PolicyFingerprint: "fp-1",
	}
}
