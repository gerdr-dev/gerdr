package server

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// App key: the phone App re-verifies with Face ID / a fingerprint instead of the browser (auth/appkey.go has the
// rules and why). Remote path only: on this computer nobody is asked to re-verify.
//
//	register   (authenticated, not due for re-verification) {pub}: right after a presence proof, never temporary
//	challenge  (public: a device due for re-verification can't reach anything else; its cookie identifies it) → {nonce, host}
//	verify     (public, same) {nonce, sig} → VerifiedAt renewed; wrong signatures count against the rate limit
//
// Bytes are standard base64. sig is ECDSA P-256 / SHA-256, ASN.1 DER, over auth.AppKeyPayload(host, device, nonce).
func (s *Server) apiAppKey(w http.ResponseWriter, r *http.Request) {
	rm := s.Remote
	if rm == nil || !rm.Config().Enabled() || !auth.FromRemote(r) {
		httpError(w, http.StatusConflict, i18n.S("App 密钥只在远程访问那条路上用", "The App key is only for remote access"))
		return
	}
	id := rm.Auth.Authenticate(r)
	if id == nil {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"error": i18n.S("这台设备还没配对", "This device isn't paired yet"), "need": "pair"})
		return
	}
	switch op := r.PathValue("op"); op {
	case "register":
		// The guard already refused a device due for re-verification (register isn't public); SetAppKey also wants
		// the presence proof to be minutes old, not just "not due yet"
		var b struct{ Pub string }
		if err := decode(r, &b); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		pub, err := base64.StdEncoding.DecodeString(b.Pub)
		if err != nil {
			httpError(w, http.StatusBadRequest, auth.ErrAppKeyInvalid.Error())
			return
		}
		if err := rm.Auth.SetAppKey(id.Device.ID, pub); err != nil {
			status := http.StatusForbidden
			if errors.Is(err, auth.ErrAppKeyInvalid) {
				status = http.StatusBadRequest
			}
			writeJSONStatus(w, status, map[string]any{"error": err.Error(), "reason": appKeyReason(err)})
			return
		}
		writeJSON(w, map[string]any{"ok": true})

	case "challenge":
		n, err := rm.Auth.AppKeyChallenge(id.Device.ID)
		if err != nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": err.Error(), "reason": "nokey"})
			return
		}
		writeJSON(w, map[string]any{"nonce": n, "host": rm.Config().Host, "deviceId": id.Device.ID})

	case "verify":
		var b struct{ Nonce, Sig string }
		if err := decode(r, &b); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		release, ok := s.gateCheck(w, r)
		if !ok {
			return
		}
		defer release()
		ip := auth.ClientIP(r)
		sig, _ := base64.StdEncoding.DecodeString(b.Sig)
		if err := rm.Auth.VerifyAppKey(id.Device.ID, b.Nonce, sig, rm.Config().Host); err != nil {
			rm.Gate.Fail(ip)
			httpError(w, http.StatusForbidden, err.Error())
			return
		}
		rm.Gate.Reset(ip)
		writeJSON(w, map[string]any{"ok": true})

	default:
		httpError(w, http.StatusNotFound, i18n.S("没有这个接口", "No such endpoint"))
	}
}

func appKeyReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrAppKeyTemp):
		return "temp"
	case errors.Is(err, auth.ErrAppKeyStale):
		return "stale"
	}
	return "invalid"
}
