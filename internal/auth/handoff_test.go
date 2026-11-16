package auth

import (
	"testing"
	"time"
)

// A handoff link signs another browser in once, as fresh as the device that made it and no fresher.
func TestHandoff(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	app, _, _ := s.Redeem(code, "iPhone GerdrApp/1", "")
	tok, err := s.MintHandoff(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	d, cookie, err := s.RedeemHandoff(tok, "iPhone · Safari", "")
	if err != nil || d.ID == app.ID || s.Authenticate(withCookie(cookie)) == nil {
		t.Fatalf("redeem: %v", err)
	}
	if !d.PasskeyAt.Equal(app.PasskeyAt) || !d.VerifiedAt.Equal(app.VerifiedAt) {
		t.Fatal("the new device should be exactly as fresh as the App")
	}
	if _, _, err := s.RedeemHandoff(tok, "again", ""); err == nil {
		t.Fatal("a handoff link works once")
	}
	if _, err := s.MintHandoff("nope"); err == nil {
		t.Fatal("only a known device can mint one")
	}
	tok2, _ := s.MintHandoff(app.ID)
	s.now = func() time.Time { return time.Now().Add(HandoffTTL + time.Second) }
	if _, _, err := s.RedeemHandoff(tok2, "late", ""); err == nil {
		t.Fatal("an expired link must not work")
	}
}
