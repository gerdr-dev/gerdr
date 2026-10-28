package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Remote 是远程访问（云端中继）的配置。和别的服务配置不一样，它**也能写在文件里**
// （<数据目录>/remote/remote.json，`gerdr remote join` 写的）：mac App 从 Finder 起，拿不到 shell 里
// export 的环境变量，只认环境变量的话 App 里永远开不了远程。环境变量照样能给，**给了就盖过文件**。
//
// 文件里有 DNS 服务商的 token（签证书用），0600。它和证书私钥、中继身份私钥是同一档：这台机器上
// 跑的 agent 读得到 —— 能读你文件的东西早就有你的 shell 了，这里能做的是别再往 agent 的**环境**里
// 漏（GERDR_* 不往下传，见 sysenv.ChildEnv；acme 签完把脱了前缀的那份撤掉）。
type Remote struct {
	RelayURL string // GERDR_RELAY_URL：中继控制面，https://herdr-relay.example.com
	Host     string // GERDR_RELAY_HOST：在中继上注册的主机名，也是 passkey 的 RPID
	// 证书：二选一。ACME 走 DNS-01 自己签（推荐，见 internal/acme）；或者自己给两个文件。
	ACMEDNS     string // GERDR_ACME_DNS：cloudflare / alidns / …
	ACMEEmail   string // GERDR_ACME_EMAIL
	ACMEStaging bool   // GERDR_ACME_STAGING=1：用 Let's Encrypt 的测试环境（调试时一定要开，正式环境一周 5 张）
	TLSCert     string // GERDR_TLS_CERT
	TLSKey      string // GERDR_TLS_KEY
	// DeviceTTL：设备凭据的滑动过期，GERDR_DEVICE_TTL_DAYS，默认 90，0 = 永不过期。
	DeviceTTL time.Duration
	// ReauthAfter：注册过 passkey 之后多久要再验一次，GERDR_REAUTH_HOURS，默认 24，0 = 不要求。
	ReauthAfter time.Duration
}

// RemoteFile 是 remote.json 的样子。Env 里是 DNS 服务商的凭据，名字不带 GERDR_ 前缀
// （`{"CLOUDFLARE_DNS_API_TOKEN": "…"}`），读的时候按 `GERDR_<名字>` 补进进程环境 —— 和直接在环境里
// 给 `GERDR_CLOUDFLARE_DNS_API_TOKEN` 是同一条路（acme.exportEnv）。
type RemoteFile struct {
	RelayURL      string            `json:"relayURL,omitempty"`
	Host          string            `json:"host,omitempty"`
	ACMEDNS       string            `json:"acmeDNS,omitempty"`
	ACMEEmail     string            `json:"acmeEmail,omitempty"`
	ACMEStaging   bool              `json:"acmeStaging,omitempty"`
	TLSCert       string            `json:"tlsCert,omitempty"`
	TLSKey        string            `json:"tlsKey,omitempty"`
	DeviceTTLDays *int              `json:"deviceTTLDays,omitempty"`
	ReauthHours   *int              `json:"reauthHours,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
}

// RemoteDir：远程访问的全部状态（设备表、passkey、中继身份私钥、ACME 账户和证书）。
func RemoteDir(dir string) string { return filepath.Join(dir, "remote") }

func remotePath(dir string) string { return filepath.Join(RemoteDir(dir), "remote.json") }

func ReadRemoteFile(dir string) (RemoteFile, error) {
	var f RemoteFile
	b, err := os.ReadFile(remotePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(b, &f)
}

func WriteRemoteFile(dir string, f RemoteFile) error {
	if err := os.MkdirAll(RemoteDir(dir), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(f, "", "  ")
	tmp := remotePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, remotePath(dir))
}

// LoadRemote：文件打底，环境变量盖上去。顺带把文件里的 DNS 凭据补进进程环境（只补环境里没有的）。
func LoadRemote(dir string) (Remote, error) {
	f, err := ReadRemoteFile(dir)
	if err != nil {
		return Remote{}, err
	}
	for k, v := range f.Env {
		k = "GERDR_" + strings.TrimPrefix(k, "GERDR_")
		if _, ok := os.LookupEnv(k); !ok {
			_ = os.Setenv(k, v)
		}
	}
	str := func(env, file string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		return file
	}
	num := func(env string, file *int, def int) int {
		if v := os.Getenv(env); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
		if file != nil {
			return *file
		}
		return def
	}
	r := Remote{
		RelayURL:    strings.TrimRight(str("GERDR_RELAY_URL", f.RelayURL), "/"),
		Host:        strings.TrimSuffix(strings.ToLower(str("GERDR_RELAY_HOST", f.Host)), "."),
		ACMEDNS:     str("GERDR_ACME_DNS", f.ACMEDNS),
		ACMEEmail:   str("GERDR_ACME_EMAIL", f.ACMEEmail),
		ACMEStaging: f.ACMEStaging || truthy(os.Getenv("GERDR_ACME_STAGING")),
		TLSCert:     str("GERDR_TLS_CERT", f.TLSCert),
		TLSKey:      str("GERDR_TLS_KEY", f.TLSKey),
	}
	if d := num("GERDR_DEVICE_TTL_DAYS", f.DeviceTTLDays, 90); d > 0 {
		r.DeviceTTL = time.Duration(d) * 24 * time.Hour
	}
	if h := num("GERDR_REAUTH_HOURS", f.ReauthHours, 24); h > 0 {
		r.ReauthAfter = time.Duration(h) * time.Hour
	}
	return r, nil
}

// Enabled：配了中继才算开了远程访问。
func (r Remote) Enabled() bool { return r.RelayURL != "" && r.Host != "" }

// TLSProblem：开了远程却没给证书来源。**远程那条路的 TLS 必须在本进程终止**（中继只转密文），
// 而且得是浏览器认的真证书 —— 自签的话手机上每次都是警告页，passkey 也按不动。
func (r Remote) TLSProblem() string {
	if r.ACMEDNS == "" && (r.TLSCert == "" || r.TLSKey == "") {
		return "还差一张 " + r.Host + " 的证书：到「设置 → 远程访问 → 证书」选这个域名的 DNS 服务商（Cloudflare / 阿里云 / 腾讯云…）、" +
			"填它的 API 凭据，gerdr 会自己去 Let's Encrypt 签；已经有证书的话在同一处指定证书和私钥两个文件"
	}
	return ""
}
