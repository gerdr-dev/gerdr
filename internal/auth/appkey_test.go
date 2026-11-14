package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"testing"
	"time"
)

func appKeyPair(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := k.PublicKey.Bytes() // uncompressed point, what the phones send
	if err != nil {
		t.Fatal(err)
	}
	return k, pub
}

func appSign(t *testing.T, k *ecdsa.PrivateKey, host, dev, nonce string) []byte {
	t.Helper()
	h := sha256.Sum256(AppKeyPayload(host, dev, nonce))
	sig, err := ecdsa.SignASN1(rand.Reader, k, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// Register only right after a presence proof, never for a temporary device; a signature over the right payload
// renews VerifiedAt, one for another host or a spent / expired nonce doesn't.
func TestAppKey(t *testing.T) {
	now := time.Now()
	s := newStore(t, Config{TTL: 90 * 24 * time.Hour, PairTTL: 30 * time.Minute})
	s.now = func() time.Time { return now }
	k, pub := appKeyPair(t)

	d, _ := s.NewDevice("iPhone GerdrApp/1", "1.2.3.4", "") // as after a passkey sign-in or an approval: fresh
	if _, err := s.AppKeyChallenge(d.ID); err != ErrAppKeyNone {
		t.Fatalf("no key yet: %v", err)
	}
	if err := s.SetAppKey(d.ID, []byte("not a key")); err != ErrAppKeyInvalid {
		t.Fatalf("garbage key: %v", err)
	}

	// A stale presence proof (a stolen cookie, an hour later) can't plant a key
	now = now.Add(StepUpWindow + time.Second)
	if err := s.SetAppKey(d.ID, pub); err != ErrAppKeyStale {
		t.Fatalf("stale device registered a key: %v", err)
	}
	now = now.Add(-StepUpWindow - time.Second)
	if err := s.SetAppKey(d.ID, pub); err != nil {
		t.Fatalf("fresh device: %v", err)
	}

	// A day later: re-verification is due; the App key renews it
	now = now.Add(25 * time.Hour)
	n, err := s.AppKeyChallenge(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyAppKey(d.ID, n, appSign(t, k, "evil.example.com", d.ID, n), "gerdr.example.com"); err != ErrAppKeyBad {
		t.Fatalf("a signature for another host passed: %v", err)
	}
	if err := s.VerifyAppKey(d.ID, n, appSign(t, k, "gerdr.example.com", d.ID, n), "gerdr.example.com"); err != ErrAppKeyBad {
		t.Fatalf("a spent nonce passed: %v", err)
	}
	n, _ = s.AppKeyChallenge(d.ID)
	if err := s.VerifyAppKey(d.ID, n, appSign(t, k, "gerdr.example.com", d.ID, n), "gerdr.example.com"); err != nil {
		t.Fatalf("good signature: %v", err)
	}
	var got Device
	for _, x := range s.Devices() {
		if x.ID == d.ID {
			got = x
		}
	}
	if !got.VerifiedAt.Equal(now) || !got.PasskeyAt.IsZero() {
		t.Fatalf("VerifiedAt should be renewed and PasskeyAt left alone: %v / %v", got.VerifiedAt, got.PasskeyAt)
	}

	// Another key (someone else's phone) doesn't verify, and a challenge expires
	other, _ := appKeyPair(t)
	n, _ = s.AppKeyChallenge(d.ID)
	if err := s.VerifyAppKey(d.ID, n, appSign(t, other, "gerdr.example.com", d.ID, n), "gerdr.example.com"); err != ErrAppKeyBad {
		t.Fatalf("another key passed: %v", err)
	}
	n, _ = s.AppKeyChallenge(d.ID)
	now = now.Add(appChallengeTTL + time.Second)
	if err := s.VerifyAppKey(d.ID, n, appSign(t, k, "gerdr.example.com", d.ID, n), "gerdr.example.com"); err != ErrAppKeyBad {
		t.Fatalf("an expired challenge passed: %v", err)
	}

	// A nonce is bound to the device it was given to
	d2, _ := s.NewDevice("iPhone GerdrApp/1", "1.2.3.4", "")
	_ = s.SetAppKey(d2.ID, pub)
	n, _ = s.AppKeyChallenge(d2.ID)
	if err := s.VerifyAppKey(d.ID, n, appSign(t, k, "gerdr.example.com", d.ID, n), "gerdr.example.com"); err != ErrAppKeyBad {
		t.Fatalf("another device's nonce passed: %v", err)
	}

	// A device that came in with a pairing code stays out (it would turn a code into a long-lived device)
	code, _ := s.MintCode()
	tmp, _, err := s.Redeem(code, "iPhone", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAppKey(tmp.ID, pub); err != ErrAppKeyTemp {
		t.Fatalf("temporary device registered a key: %v", err)
	}

	// Revoking the device drops its key with it
	s.Revoke(d.ID)
	if _, err := s.AppKeyChallenge(d.ID); err != ErrAppKeyNone {
		t.Fatalf("revoked device still has a key: %v", err)
	}
}
