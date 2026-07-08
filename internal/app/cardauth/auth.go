package cardauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const tokenPrefix = "bridge_cb.v1"

type Key struct {
	Version int
	Secret  string
	Retired bool
}

type Options struct {
	Keys        []Key
	NonceStore  *NonceStore
	Now         func() time.Time
	CreateNonce func() (string, error)
}

type Auth struct {
	keys        []Key
	nonceStore  *NonceStore
	now         func() time.Time
	createNonce func() (string, error)
}

type SignInput struct {
	RunID             string
	Scope             string
	ChatID            string
	OperatorOpenID    string
	Action            string
	PolicyFingerprint string
	TTL               time.Duration
}

type VerifyExpected struct {
	RunID             string
	Scope             string
	ChatID            string
	OperatorOpenID    string
	Action            string
	PolicyFingerprint string
}

type Payload struct {
	RunID             string `json:"r"`
	Scope             string `json:"s"`
	ChatID            string `json:"c"`
	OperatorOpenID    string `json:"o"`
	Action            string `json:"a"`
	ExpiresAt         int64  `json:"exp"`
	PolicyFingerprint string `json:"fp"`
	Nonce             string `json:"n"`
	KeyVersion        int    `json:"kv"`
}

type VerifyReason string

const (
	VerifyMalformed       VerifyReason = "malformed"
	VerifyUnknownKey      VerifyReason = "unknown-key"
	VerifyBadSignature    VerifyReason = "bad-signature"
	VerifyExpired         VerifyReason = "expired"
	VerifyContextMismatch VerifyReason = "context-mismatch"
	VerifyNonceReplay     VerifyReason = "nonce-replay"
	VerifyNonceRevoked    VerifyReason = "nonce-revoked"
)

type VerifyResult struct {
	OK      bool
	Payload Payload
	Reason  VerifyReason
}

func New(options Options) (*Auth, error) {
	if len(options.Keys) == 0 {
		return nil, errors.New("at least one callback key is required")
	}
	keys := append([]Key(nil), options.Keys...)
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j].Version < keys[i].Version {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	createNonce := options.CreateNonce
	if createNonce == nil {
		createNonce = randomNonce
	}
	store := options.NonceStore
	if store == nil {
		store = NewNonceStore("")
	}
	return &Auth{
		keys:        keys,
		nonceStore:  store,
		now:         now,
		createNonce: createNonce,
	}, nil
}

func (a *Auth) Sign(input SignInput) (string, error) {
	key, ok := a.signingKey()
	if !ok {
		return "", errors.New("no active callback signing key")
	}
	nonce, err := a.createNonce()
	if err != nil {
		return "", err
	}
	payload := Payload{
		RunID:             input.RunID,
		Scope:             input.Scope,
		ChatID:            input.ChatID,
		OperatorOpenID:    input.OperatorOpenID,
		Action:            input.Action,
		ExpiresAt:         a.now().Add(input.TTL).UnixMilli(),
		PolicyFingerprint: input.PolicyFingerprint,
		Nonce:             nonce,
		KeyVersion:        key.Version,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return tokenPrefix + "." + encoded + "." + sign(encoded, key.Secret), nil
}

func (a *Auth) Verify(token string, expected VerifyExpected) VerifyResult {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0]+"."+parts[1] != tokenPrefix {
		return failed(VerifyMalformed)
	}
	encodedPayload := parts[2]
	signature := parts[3]
	if encodedPayload == "" || signature == "" {
		return failed(VerifyMalformed)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return failed(VerifyMalformed)
	}
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil || !validPayload(payload) {
		return failed(VerifyMalformed)
	}
	key, ok := a.key(payload.KeyVersion)
	if !ok {
		return failed(VerifyUnknownKey)
	}
	if !signatureMatches(signature, sign(encodedPayload, key.Secret)) {
		return failed(VerifyBadSignature)
	}
	if payload.ExpiresAt <= a.now().UnixMilli() {
		return failed(VerifyExpired)
	}
	if !matchesExpected(payload, expected) {
		return failed(VerifyContextMismatch)
	}
	if state, ok := a.nonceStore.State(payload.Nonce); ok {
		if state == NonceRevoked {
			return failed(VerifyNonceRevoked)
		}
		return failed(VerifyNonceReplay)
	}
	if !a.nonceStore.Consume(payload.Nonce) {
		return failed(VerifyNonceReplay)
	}
	return VerifyResult{OK: true, Payload: payload}
}

func (a *Auth) signingKey() (Key, bool) {
	for i := len(a.keys) - 1; i >= 0; i-- {
		if !a.keys[i].Retired {
			return a.keys[i], true
		}
	}
	return Key{}, false
}

func (a *Auth) key(version int) (Key, bool) {
	for _, key := range a.keys {
		if key.Version == version {
			return key, true
		}
	}
	return Key{}, false
}

func matchesExpected(payload Payload, expected VerifyExpected) bool {
	return payload.RunID == expected.RunID &&
		payload.Scope == expected.Scope &&
		payload.ChatID == expected.ChatID &&
		payload.OperatorOpenID == expected.OperatorOpenID &&
		payload.Action == expected.Action &&
		payload.PolicyFingerprint == expected.PolicyFingerprint
}

func validPayload(payload Payload) bool {
	return payload.RunID != "" &&
		payload.Scope != "" &&
		payload.ChatID != "" &&
		payload.OperatorOpenID != "" &&
		payload.Action != "" &&
		payload.ExpiresAt != 0 &&
		payload.PolicyFingerprint != "" &&
		payload.Nonce != "" &&
		payload.KeyVersion != 0
}

func sign(payload string, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func signatureMatches(actual string, expected string) bool {
	actualBytes := []byte(actual)
	expectedBytes := []byte(expected)
	return len(actualBytes) == len(expectedBytes) &&
		subtle.ConstantTimeCompare(actualBytes, expectedBytes) == 1
}

func randomNonce() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func failed(reason VerifyReason) VerifyResult {
	return VerifyResult{OK: false, Reason: reason}
}
