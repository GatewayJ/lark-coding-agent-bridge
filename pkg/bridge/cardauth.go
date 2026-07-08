package bridge

import (
	"errors"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardauth"
)

var ErrNilCallbackAuth = errors.New("callback auth is nil")

type CallbackKey struct {
	Version int
	Secret  string
	Retired bool
}

type CallbackSignInput struct {
	RunID             string
	Scope             string
	ChatID            string
	OperatorOpenID    string
	Action            string
	PolicyFingerprint string
	TTL               time.Duration
}

type CallbackVerifyExpected struct {
	RunID             string
	Scope             string
	ChatID            string
	OperatorOpenID    string
	Action            string
	PolicyFingerprint string
}

type CallbackPayload struct {
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

type CallbackVerifyReason string

const (
	CallbackMalformed       CallbackVerifyReason = "malformed"
	CallbackUnknownKey      CallbackVerifyReason = "unknown-key"
	CallbackBadSignature    CallbackVerifyReason = "bad-signature"
	CallbackExpired         CallbackVerifyReason = "expired"
	CallbackContextMismatch CallbackVerifyReason = "context-mismatch"
	CallbackNonceReplay     CallbackVerifyReason = "nonce-replay"
	CallbackNonceRevoked    CallbackVerifyReason = "nonce-revoked"
)

type CallbackVerifyResult struct {
	OK      bool
	Payload CallbackPayload
	Reason  CallbackVerifyReason
}

type CallbackAuthOptions struct {
	Keys           []CallbackKey
	NonceStorePath string
	Now            func() time.Time
	CreateNonce    func() (string, error)
}

type CallbackAuth struct {
	auth  *cardauth.Auth
	store *cardauth.NonceStore
}

func NewCallbackAuth(options CallbackAuthOptions) (*CallbackAuth, error) {
	keys := make([]cardauth.Key, 0, len(options.Keys))
	for _, key := range options.Keys {
		keys = append(keys, cardauth.Key{
			Version: key.Version,
			Secret:  key.Secret,
			Retired: key.Retired,
		})
	}
	store := cardauth.NewNonceStore(options.NonceStorePath)
	if err := store.Load(); err != nil {
		return nil, err
	}
	auth, err := cardauth.New(cardauth.Options{
		Keys:        keys,
		NonceStore:  store,
		Now:         options.Now,
		CreateNonce: options.CreateNonce,
	})
	if err != nil {
		return nil, err
	}
	return &CallbackAuth{auth: auth, store: store}, nil
}

func (a *CallbackAuth) Sign(input CallbackSignInput) (string, error) {
	if a == nil || a.auth == nil {
		return "", ErrNilCallbackAuth
	}
	return a.auth.Sign(cardauth.SignInput(input))
}

func (a *CallbackAuth) Verify(token string, expected CallbackVerifyExpected) CallbackVerifyResult {
	if a == nil || a.auth == nil {
		return CallbackVerifyResult{Reason: CallbackMalformed}
	}
	result := a.auth.Verify(token, cardauth.VerifyExpected(expected))
	return CallbackVerifyResult{
		OK:      result.OK,
		Payload: CallbackPayload(result.Payload),
		Reason:  CallbackVerifyReason(result.Reason),
	}
}

func (a *CallbackAuth) RevokeNonce(nonce string) {
	if a == nil || a.store == nil {
		return
	}
	a.store.Revoke(nonce)
}

func (a *CallbackAuth) Flush() error {
	if a == nil || a.store == nil {
		return ErrNilCallbackAuth
	}
	return a.store.Flush()
}
