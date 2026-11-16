package auth

import (
	"crypto/subtle"
	"errors"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Handoff: a signed-in device opens this Gerdr in another browser on the same phone without pairing again.
//
// Why: the iOS App can't run passkeys in its WKWebView (docs/dev/IOS.md), so "add a passkey" sends the person to
// Safari, which has its own cookies and used to land on the pairing page: a second pairing for something they had
// just done. Now the App mints a one-time link and Safari redeems it for a device of its own.
//
// The link carries a 256-bit token, single use, valid HandoffTTL, held only in memory (a restart drops them). The new
// device copies VerifiedAt / PasskeyAt from the one that minted it, never more: what the App could do right now,
// Safari can do, and a stale App can't make a fresh Safari. It holds no more than the cookie that minted it, and
// "revoke all" removes both.
const HandoffTTL = 2 * time.Minute

type handoff struct {
	hash string // of the token
	dev  string // device that minted it
	exp  time.Time
}

var ErrBadHandoff = errors.New(i18n.S("这个链接已经用过或者过期了：回 App 里再点一次", "this link was already used or has expired: tap it again in the App"))

// MintHandoff returns a token for HandoffURL; devID must be a signed-in, non-peer device.
func (s *Store) MintHandoff(devID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var src *Device
	for _, d := range s.devs {
		if d.ID == devID {
			src = d
		}
	}
	if src == nil || src.Peer || src.Expired(now) {
		return "", ErrBadHandoff
	}
	kept := s.handoffs[:0]
	for _, h := range s.handoffs {
		if now.Before(h.exp) {
			kept = append(kept, h)
		}
	}
	tok := randToken()
	s.handoffs = append(kept, handoff{hash: hashToken(tok), dev: devID, exp: now.Add(HandoffTTL)})
	return tok, nil
}

// RedeemHandoff turns a handoff token into a new device for this browser, as fresh as the one that minted it.
func (s *Store) RedeemHandoff(tok, ua, ip string) (*Device, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now, h := s.now(), hashToken(tok)
	hit := -1
	for i, x := range s.handoffs {
		if subtle.ConstantTimeCompare([]byte(x.hash), []byte(h)) == 1 && now.Before(x.exp) {
			hit = i
		}
	}
	if hit < 0 {
		return nil, "", ErrBadHandoff
	}
	from := s.handoffs[hit].dev
	s.handoffs = append(s.handoffs[:hit], s.handoffs[hit+1:]...)
	var src *Device
	for _, d := range s.devs {
		if d.ID == from {
			src = d
		}
	}
	if src == nil || src.Expired(now) {
		return nil, "", ErrBadHandoff
	}
	d, token := s.newLocked(ua, ip, "")
	d.VerifiedAt, d.PasskeyAt = src.VerifiedAt, src.PasskeyAt
	if src.Temp {
		d.Temp, d.Expires = true, src.Expires
	}
	s.flushLocked()
	return d, token, nil
}
