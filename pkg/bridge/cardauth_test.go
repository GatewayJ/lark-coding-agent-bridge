package bridge

import (
	"errors"
	"testing"
)

func TestNilCallbackAuthReturnsErrors(t *testing.T) {
	var auth *CallbackAuth
	if _, err := auth.Sign(CallbackSignInput{}); !errors.Is(err, ErrNilCallbackAuth) {
		t.Fatalf("Sign error = %v, want ErrNilCallbackAuth", err)
	}
	if got := auth.Verify("token", CallbackVerifyExpected{}); got.OK || got.Reason != CallbackMalformed {
		t.Fatalf("Verify result = %#v, want malformed", got)
	}
	auth.RevokeNonce("nonce")
	if err := auth.Flush(); !errors.Is(err, ErrNilCallbackAuth) {
		t.Fatalf("Flush error = %v, want ErrNilCallbackAuth", err)
	}
}
