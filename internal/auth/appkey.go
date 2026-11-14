package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

/* ------------------------------------------- App key: re-verify with Face ID, no browser */

// The phone Apps can't use passkeys for a person's own domain inside the App on iOS (the App's signature would have
// to list it, docs/dev/IOS.md "Passkey"), so re-verifying every ReauthAfter meant the browser device flow each day.
// Instead the App keeps its own key for this device: a P-256 key made in the phone's secure hardware (iOS Secure
// Enclave, Android Keystore), whose private half never leaves it and which needs Face ID / a fingerprint for every
// signature. The server keeps the public half on the Device:
//
//	register   right after the device proved presence (VerifiedAt within StepUpWindow: a passkey, an approval, a
//	           pairing); never for a Temp device. A stolen cookie alone has no fresh VerifiedAt, so it can't plant
//	           its own key and then skip re-verification forever.
//	challenge  a single-use nonce, for a device that has a key (its cookie identifies it, also when re-verification
//	           is due)
//	verify     an ECDSA signature over AppKeyPayload(host, device, nonce) → VerifiedAt = now, as a passkey would.
//	           The host is in what's signed: a signature a look-alike server collects is no good at the real one.
//
// It renews VerifiedAt only. PasskeyAt stays a real passkey (adding one more needs that), and a Temp device stays
// temporary.

// StepUpWindow: how recent the presence proof must be to register an App key (and what the server's own step-up
// window is, server.stepUpWindow).
const StepUpWindow = 5 * time.Minute

// appChallengeTTL: a challenge waits this long for its signature (one Face ID prompt).
const appChallengeTTL = 2 * time.Minute

// appChallengeMax: outstanding challenges at most. Asking needs a valid device cookie, but still no unbounded growth.
const appChallengeMax = 64

var (
	ErrAppKeyNone    = errors.New(i18n.S("这台设备没有登记 App 密钥", "This device has no App key"))
	ErrAppKeyBad     = errors.New(i18n.S("App 密钥验证没通过", "The App key check failed"))
	ErrAppKeyStale   = errors.New(i18n.S("先用 passkey 或浏览器批准验证一次，再登记 App 密钥", "Verify with a passkey or a browser approval first, then register the App key"))
	ErrAppKeyTemp    = errors.New(i18n.S("配对码进来的临时设备不能登记 App 密钥", "A temporary device (paired with a code) can't register an App key"))
	ErrAppKeyInvalid = errors.New(i18n.S("App 密钥的格式不对（要 P-256 公钥）", "Bad App key (expected a P-256 public key)"))
)

// AppKey is a phone App's hardware-bound public key for one device.
type AppKey struct {
	// Pub: the uncompressed P-256 point (0x04 || X || Y, 65 bytes), what iOS SecKeyCopyExternalRepresentation and
	// Android's ECPoint give.
	Pub   []byte    `json:"pub"`
	Added time.Time `json:"added"`
}

type appChallenge struct {
	nonce, device string
	exp           time.Time
}

// AppKeyPayload is what the App signs (ECDSA P-256 over its SHA-256). Versioned and line-separated so no field can
// run into the next.
func AppKeyPayload(host, deviceID, nonce string) []byte {
	return []byte("gerdr-appkey-v1\n" + host + "\n" + deviceID + "\n" + nonce)
}

func parseAppKey(pub []byte) (*ecdsa.PublicKey, error) {
	k, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
	if err != nil {
		return nil, ErrAppKeyInvalid
	}
	return k, nil
}

// SetAppKey registers (or replaces) the App key of a device. The caller passes the device as just authenticated;
// the rules (fresh presence, not temporary) are checked here against the stored device.
func (s *Store) SetAppKey(deviceID string, pub []byte) error {
	if _, err := parseAppKey(pub); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, d := range s.devs {
		if d.ID != deviceID {
			continue
		}
		switch {
		case d.Expired(now):
			return ErrAppKeyStale
		case d.Temp:
			return ErrAppKeyTemp
		case d.VerifiedAt.IsZero() || now.Sub(d.VerifiedAt) > StepUpWindow:
			return ErrAppKeyStale
		}
		d.AppKey = &AppKey{Pub: append([]byte(nil), pub...), Added: now}
		s.flushLocked()
		return nil
	}
	return ErrAppKeyStale
}

// AppKeyChallenge gives a single-use nonce for a device with an App key.
func (s *Store) AppKeyChallenge(deviceID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	has := false
	for _, d := range s.devs {
		if d.ID == deviceID && d.AppKey != nil && !d.Expired(now) {
			has = true
		}
	}
	if !has {
		return "", ErrAppKeyNone
	}
	keep := s.appChal[:0]
	for _, c := range s.appChal {
		if now.Before(c.exp) {
			keep = append(keep, c)
		}
	}
	s.appChal = keep
	if len(s.appChal) >= appChallengeMax {
		s.appChal = s.appChal[1:] // the oldest gives way: a busy attacker can't lock the owner out
	}
	n := randToken()
	s.appChal = append(s.appChal, appChallenge{nonce: n, device: deviceID, exp: now.Add(appChallengeTTL)})
	return n, nil
}

// VerifyAppKey checks a signature over AppKeyPayload(host, deviceID, nonce) and, when it holds, records presence
// (VerifiedAt). The nonce is spent either way.
func (s *Store) VerifyAppKey(deviceID, nonce string, sig []byte, host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	found := false
	for i, c := range s.appChal {
		if subtle.ConstantTimeCompare([]byte(c.nonce), []byte(nonce)) == 1 {
			s.appChal = append(s.appChal[:i], s.appChal[i+1:]...)
			found = c.device == deviceID && now.Before(c.exp)
			break
		}
	}
	if !found {
		return ErrAppKeyBad
	}
	for _, d := range s.devs {
		if d.ID != deviceID || d.AppKey == nil || d.Expired(now) {
			continue
		}
		k, err := parseAppKey(d.AppKey.Pub)
		if err != nil {
			return ErrAppKeyBad
		}
		h := sha256.Sum256(AppKeyPayload(host, deviceID, nonce))
		if !ecdsa.VerifyASN1(k, h[:], sig) {
			return ErrAppKeyBad
		}
		d.VerifiedAt = now
		s.flushLocked()
		return nil
	}
	return ErrAppKeyBad
}
