// Package tailscale gets a certificate for this machine's tailnet name (`<machine>.<tailnet>.ts.net`) from the
// Tailscale client already running here: the no-domain way into remote access (docs/dev/REMOTE.md "Tailscale").
//
// It drives the `tailscale` CLI instead of linking tailscale.com's LocalAPI client: the CLI finds the daemon on
// every platform (on macOS the GUI app's LocalAPI is behind a port + token the CLI knows how to discover), and the
// module would pull in a large dependency tree for two calls. The cert goes to stdout (`--cert-file -`): the Mac
// App Store build's CLI is sandboxed, and files it writes land in its container, not where we asked.
//
// tailscaled keeps the certificate and renews it itself (Let's Encrypt via Tailscale's DNS); asking again returns
// the cached one or a renewed one, so renewing on our side is just asking every so often.
package tailscale

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/sysenv"
)

// AdminDNS is where a tailnet admin turns on HTTPS certificates (off by default).
const AdminDNS = "https://login.tailscale.com/admin/dns"

// Info is what the settings page needs to offer Tailscale: the name to use, or why it can't.
type Info struct {
	Name string `json:"name,omitempty"` // this machine's tailnet name, no trailing dot
	// Problem: why the name can't get a certificate right now (not installed, not connected, HTTPS off); empty = ready.
	Problem string `json:"problem,omitempty"`
}

// bin finds the CLI: PATH (a login shell's, the App starts with launchd's), then where the installers put it.
func bin() (string, error) {
	if p, err := sysenv.FindBin("tailscale"); err == nil {
		return p, nil
	}
	var cands []string
	switch runtime.GOOS {
	case "darwin":
		// The GUI app's own binary is the CLI when given arguments (both the App Store and the standalone build)
		cands = []string{"/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale"}
	case "windows":
		cands = []string{filepath.Join(os.Getenv("ProgramFiles"), "Tailscale", "tailscale.exe")}
	default:
		cands = []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale"}
	}
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New(i18n.S("这台电脑上没找到 Tailscale：先装上并登录（tailscale.com/download）", "Tailscale isn't installed on this computer: install it and sign in first (tailscale.com/download)"))
}

func run(ctx context.Context, args ...string) ([]byte, error) {
	b, err := bin()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, b, args...)
	hideConsole(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, explain(msg)
	}
	return stdout.Bytes(), nil
}

// explain turns the CLI's wording into what to do about it.
func explain(msg string) error {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "https cert support is not enabled") || strings.Contains(low, "not enabled/configured"):
		return errors.New(httpsOff())
	case strings.Contains(low, "access denied") || strings.Contains(low, "permission denied"):
		// Linux: only root or the operator may ask tailscaled for a certificate
		return fmt.Errorf(i18n.S("Tailscale 不让这个用户拿证书：跑一次 sudo tailscale set --operator=$USER（%s）", "Tailscale won't give this user a certificate: run sudo tailscale set --operator=$USER once (%s)"), msg)
	case strings.Contains(low, "failed to connect to local tailscale") || strings.Contains(low, "is tailscale running"):
		return errors.New(i18n.S("Tailscale 没在运行：打开它并登录", "Tailscale isn't running: open it and sign in"))
	}
	return errors.New("tailscale: " + msg)
}

func httpsOff() string {
	return i18n.S("这个 tailnet 没开 HTTPS 证书：到 "+AdminDNS+" 页面底部打开 HTTPS Certificates（MagicDNS 也要开着）",
		"HTTPS certificates are off for this tailnet: turn on HTTPS Certificates at the bottom of "+AdminDNS+" (MagicDNS must be on too)")
}

type status struct {
	BackendState string
	Self         *struct{ DNSName string }
	CertDomains  []string
}

// Detect reads this machine's tailnet name and whether it can get a certificate.
func Detect(ctx context.Context) Info {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := run(ctx, "status", "--json")
	if err != nil {
		return Info{Problem: err.Error()}
	}
	var st status
	if err := json.Unmarshal(out, &st); err != nil {
		return Info{Problem: "tailscale status: " + err.Error()}
	}
	return parse(st)
}

func parse(st status) Info {
	var in Info
	if st.Self != nil {
		in.Name = strings.ToLower(strings.TrimSuffix(st.Self.DNSName, "."))
	}
	switch {
	case st.BackendState != "Running":
		in.Problem = fmt.Sprintf(i18n.S("Tailscale 没连上（%s）：打开它并登录", "Tailscale isn't connected (%s): open it and sign in"), st.BackendState)
	case in.Name == "":
		in.Problem = i18n.S("这台机器在 tailnet 里没有名字：在 Tailscale 后台的 DNS 页打开 MagicDNS", "this machine has no tailnet name: turn on MagicDNS on the DNS page of the Tailscale admin console")
	case len(st.CertDomains) == 0:
		in.Problem = httpsOff()
	}
	return in
}

// Cert asks tailscaled for name's certificate and writes it to certFile / keyFile, touching them only when it
// changed (the TLS side reloads on mtime). changed reports whether it wrote.
func Cert(ctx context.Context, name, certFile, keyFile string) (changed bool, err error) {
	// The first one is issued on the spot (an ACME DNS challenge on Tailscale's side): tens of seconds
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := run(ctx, "cert", "--cert-file", "-", "--key-file", "-", name)
	if err != nil {
		return false, err
	}
	certPEM, keyPEM, err := split(out)
	if err != nil {
		return false, err
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return false, fmt.Errorf("tailscale cert: %w", err)
	}
	if old, err := os.ReadFile(certFile); err == nil && bytes.Equal(old, certPEM) {
		if oldKey, err := os.ReadFile(keyFile); err == nil && bytes.Equal(oldKey, keyPEM) {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return false, err
	}
	// Key first: the reload is keyed on the cert file's mtime, so the pair is complete once the cert lands
	if err := writeAtomic(keyFile, keyPEM, 0o600); err != nil {
		return false, err
	}
	return true, writeAtomic(certFile, certPEM, 0o644)
}

// split separates `tailscale cert`'s stdout (both files to "-": the chain, then the key) into the two files.
func split(b []byte) (certPEM, keyPEM []byte, err error) {
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			break
		}
		switch {
		case blk.Type == "CERTIFICATE":
			certPEM = append(certPEM, pem.EncodeToMemory(blk)...)
		case strings.HasSuffix(blk.Type, "PRIVATE KEY"):
			keyPEM = pem.EncodeToMemory(blk)
		}
	}
	if certPEM == nil || keyPEM == nil {
		return nil, nil, errors.New(i18n.S("tailscale cert 没给出证书和私钥", "tailscale cert returned no certificate and key"))
	}
	return certPEM, keyPEM, nil
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
