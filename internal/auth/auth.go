// Package auth 管「从远程（云端中继）进来的人是谁」：一次性配对码 + 每台设备一份长期凭据 + passkey。
//
// 照搬 herdr-web 的 internal/auth（设计理由在它的 docs/dev/SECURITY.md），gerdr 这边的差别只有一条：
// **本机口不走这里**。本机口（127.0.0.1:7870）照旧认 ~/.gerdr/token —— mac App、`gerdr session`、
// agent 自己都靠它；远程那条路**一律不认这个令牌**，见 docs/dev/REMOTE.md「两条路两套凭据」。
//
// 设计目标只有一句：**一台设备配一次，之后再也不用管**。所以：
//
//   - 长期凭据绑**设备**（cookie 里的随机令牌），不绑 IP。IP 会被复用、也会自己变（换 Wi-Fi、
//     蜂窝），绑了就是「既不安全又要反复配对」两头都输。同理不绑 UA —— 系统一升级就得重配。
//   - 落盘只存 sha256。这台机器上跑的 agent 天天读不可信内容，凭据文件被 prompt
//     injection 读走是日常风险，不是理论风险。
//   - 配对码只在内存里、5 分钟过期、用一次就废。所以磁盘上不存在任何能直接远程登录的明文。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CookieName 前端读不到（HttpOnly），只有服务端用。和本机口那个按令牌算的 cookie 名字不一样：
// 两个 origin 本来就不共享 cookie，名字分开只是让人在开发者工具里一眼分得清。
const CookieName = "gerdr_dev"

// CodeTTL：配对码的有效期。短到「拍走截图也没用」，长到够你走回沙发上扫码。
const CodeTTL = 5 * time.Minute

// 配对码的字母表去掉了 0/1/I/L/O —— 这个码是要在终端里念给自己、在手机上手输的。
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

const codeLen = 8

// flushEvery：LastSeen 变化不值得每个请求写一次盘（界面上的请求很密）。
const flushEvery = 30 * time.Second

type Device struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Hash     string    `json:"hash"` // sha256(令牌明文)，明文只在签发那一刻存在
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
	LastIP   string    `json:"lastIp"`
	Expires  time.Time `json:"expires"` // 零值 = 永不过期
	// PasskeyAt 是上一次**真的过了一把 passkey** 的时刻。
	//
	// ⚠️ 为什么不能复用 VerifiedAt：**配对本身就会把 VerifiedAt 设成当下**（配对码本身就是一次
	// 「你在机器前」的证明）。于是「再注册一把 passkey 要求新鲜认证」如果按 VerifiedAt 判，刚拿
	// 配对码进来的人**天然就是新鲜的** —— 那道门对他等于不存在，而他恰恰是这道门唯一要拦的人。
	// 两个时刻必须分开记：一个回答「这份凭据怎么来的」，一个回答「人刚刚用生物因子证明过自己」。
	PasskeyAt time.Time `json:"passkeyAt"`
	// VerifiedAt 是上一次「人证明了自己在场」的时刻：配对成功，或者过了一次 passkey。
	// 注册过 passkey 之后，服务端会要求这个时间足够新，否则要求重新验一次 ——
	// 这是把「cookie 被偷」的可用窗口从整个 TTL 压到一天的那个机制。
	VerifiedAt time.Time `json:"verifiedAt"`
}

func (d Device) Expired(now time.Time) bool {
	return !d.Expires.IsZero() && now.After(d.Expires)
}

type Config struct {
	Dir string
	// TTL 是滑动过期（每次用都续期）。0 = 永不过期。
	TTL time.Duration
}

type Store struct {
	cfg Config

	mu      sync.Mutex
	devs    []*Device
	codes   map[string]time.Time // 配对码 → 过期时刻
	cli     []*CLIRequest        // 等批准的 App 登录（cliapprove.go）
	dirty   bool
	flushed time.Time
	guard   tamperGuard

	now func() time.Time // 测试用
}

func New(cfg Config) (*Store, error) {
	s := &Store{cfg: cfg, codes: map[string]time.Time{}, now: time.Now}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

/* ------------------------------------------------------------------ 落盘 */

func (s *Store) file() string { return filepath.Join(s.cfg.Dir, "devices.json") }

func (s *Store) load() error {
	b, err := os.ReadFile(s.file())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var w struct{ Devices []*Device }
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	now := s.now()
	for _, d := range w.Devices {
		if !d.Expired(now) {
			s.devs = append(s.devs, d)
		}
	}
	s.guard.note(s.file())
	return nil
}

func (s *Store) flushLocked() {
	s.dirty = false
	s.flushed = s.now()
	if err := os.MkdirAll(s.cfg.Dir, 0o700); err != nil {
		return
	}
	b, err := json.MarshalIndent(struct{ Devices []*Device }{s.devs}, "", "  ")
	if err != nil {
		return
	}
	// 先写临时文件再 rename：崩在中间也不会留下一个解不开的 devices.json
	// （解不开就等于所有设备一起掉线）
	tmp := s.file() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.file())
	}
	s.guard.note(s.file())
}

// Flush 把攒着的 LastSeen 落盘。
func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty {
		s.flushLocked()
	}
}

/* ------------------------------------------------------------------ 配对码 */

// MintCode 出一个一次性配对码。旧的不作废 —— 连着出两次时，先扫哪个都能用。
//
// **只能从本机口调**（server 那边把关）：配对码造出来的是一份独立的、不随创造者一起被撤销的凭据，
// 远程设备能出码的话，手机被人拿去一次就能留下一台你踢不掉的设备（herdr-web SECURITY.md §11）。
func (s *Store) MintCode() (string, time.Time) {
	code := randCode()
	exp := s.now().Add(CodeTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for c, e := range s.codes {
		if s.now().After(e) {
			delete(s.codes, c)
		}
	}
	s.codes[code] = exp
	return code, exp
}

var ErrBadCode = errors.New("配对码不对或者已经过期了")

// Redeem 用配对码换一台设备的长期凭据。返回的令牌明文只在这一次出现，之后只有哈希。
func (s *Store) Redeem(code, ua, ip string) (*Device, string, error) {
	code = normalizeCode(code)
	if code == "" {
		return nil, "", ErrBadCode
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 遍历比对而不是直接查 map：码只有 40 位，别把「猜对了几位」这种信息泄在时间里
	var hit string
	now := s.now()
	for c, exp := range s.codes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(code)) == 1 && now.Before(exp) {
			hit = c
		}
	}
	if hit == "" {
		return nil, "", ErrBadCode
	}
	delete(s.codes, hit) // 用一次就废
	d, token := s.newLocked(ua, ip, "")
	return d, token, nil
}

// NewDevice 不走配对码直接签一台设备。只给 passkey 登录用（新设备过了 passkey 就直接换一份凭据，
// 不用回机器前拿码）。别拿它当别的认证路径。
func (s *Store) NewDevice(ua, ip, note string) (*Device, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newLocked(ua, ip, note)
}

func (s *Store) newLocked(ua, ip, note string) (*Device, string) {
	now := s.now()
	token := randToken()
	label := LabelFromUA(ua)
	if note != "" {
		label += "（" + note + "）"
	}
	d := &Device{ID: randID(), Label: label, Hash: hashToken(token), Created: now, LastSeen: now, LastIP: ip, VerifiedAt: now}
	if s.cfg.TTL > 0 {
		d.Expires = now.Add(s.cfg.TTL)
	}
	s.devs = append(s.devs, d)
	s.flushLocked()
	return d, token
}

/* ------------------------------------------------------------------ 设备 */

func (s *Store) Devices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.devs))
	for _, d := range s.devs {
		out = append(out, *d)
	}
	return out
}

// Revoke 支持 ID 前缀，命令行上不用抄全。
func (s *Store) Revoke(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.devs {
		if d.ID == id || (len(id) >= 4 && strings.HasPrefix(d.ID, id)) {
			label := d.Label
			s.devs = append(s.devs[:i], s.devs[i+1:]...)
			s.flushLocked()
			return label, true
		}
	}
	return "", false
}

func (s *Store) RevokeAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.devs)
	s.devs = nil
	s.flushLocked()
	return n
}

/* ------------------------------------------------------------------ 认证 */

// Ident 是「这个远程请求是哪台设备」。只有 cookie 一种来源，所以都是浏览器自动带的（要防 CSRF）。
type Ident struct {
	Label  string
	Device *Device
	// VerifiedAt / PasskeyAt 透出来给上层判断「要不要重新验一次」「能不能再加一把 passkey」。
	// 判断放在 server 那边做，因为「有没有注册过 passkey」是它才知道的事。
	VerifiedAt time.Time
	PasskeyAt  time.Time
}

// Authenticate 只认设备 cookie。**没有 Bearer、没有「本机免认证」**：这个函数只服务中继那条路，
// 那条路上的源地址是中继说的、令牌文件是 agent 读得到的，两样都不能当身份。
func (s *Store) Authenticate(r *http.Request) *Ident {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	d := s.lookup(c.Value, ClientIP(r))
	if d == nil {
		return nil
	}
	return &Ident{Label: d.Label, Device: d, VerifiedAt: d.VerifiedAt, PasskeyAt: d.PasskeyAt}
}

func (s *Store) lookup(token, ip string) *Device {
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, d := range s.devs {
		if subtle.ConstantTimeCompare([]byte(d.Hash), []byte(h)) == 1 {
			if d.Expired(now) {
				return nil
			}
			d.LastSeen = now
			if ip != "" {
				d.LastIP = ip
			}
			if s.cfg.TTL > 0 {
				d.Expires = now.Add(s.cfg.TTL) // 滑动续期：天天用的设备永远不掉
			}
			s.dirty = true
			if now.Sub(s.flushed) > flushEvery {
				s.flushLocked()
			}
			c := *d
			return &c
		}
	}
	return nil
}

// ClientIP 就是对端地址，**不看 X-Forwarded-For**：前面没有任何可信的反代，XFF 只可能是客户端
// 自己写的 —— 信了它，谁都能换一个「源 IP」把限速绕干净。中继那条路上的真实 IP 是隧道带过来、
// 填进 RemoteAddr 的（relay.srcConn）。
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// CheckTampered 看一眼凭据文件有没有被外部改过，被改过就调一次 alert。
func (s *Store) CheckTampered(alert func(string)) {
	s.guard.check("设备凭据文件", alert)
}

// MarkVerified 记一次刚做过的生物验证（passkey 断言成功）。
//
// 调用点只有 passkey 的 login/finish 和 register/finish 两处 —— 也就是说 PasskeyAt
// 这个时刻只可能由一次真的 passkey 断言（或者本机亲手批准，GrantStepUp）写出来，配对那条路碰不到它。
// StepUpNeeded 靠的就是这一点。
func (s *Store) MarkVerified(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devs {
		if d.ID == id {
			now := s.now()
			d.VerifiedAt = now
			d.PasskeyAt = now
			s.flushLocked()
			return
		}
	}
}

// GrantStepUp：本机批准了这台设备加 passkey（Approvals）。只写 PasskeyAt —— 它回答的是「能不能再加一把
// passkey」，本机那个人亲手点的批准和刚过了一把 passkey 同样算数；VerifiedAt 不动，重新验证的要求照旧。
// 调用点只有本机口上的 /api/remote/stepup/approve，远程那条路碰不到（remoteGuard 拦着）。
func (s *Store) GrantStepUp(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devs {
		if d.ID == id {
			d.PasskeyAt = s.now()
			s.flushLocked()
			return true
		}
	}
	return false
}

/* ------------------------------------------------------------------ cookie */

// IssueCookie 永远带 Secure：远程那条路一定是 https（TLS 在本进程终止）。
func (s *Store) IssueCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:  CookieName,
		Value: token,
		Path:  "/",
		// SameSite=Strict：外站发起的请求一律带不上这个 cookie。代价是从别的 app 点链接
		// 进来时首次导航不带 cookie —— 那一下拿到的是配对页，页面上的 whoami 会带着 cookie
		// 再问一次，认出来就直接刷新进去（见 pairpage.html）。
		SameSite: http.SameSiteStrictMode,
		HttpOnly: true,
		Secure:   true,
		MaxAge:   int(s.cookieMaxAge().Seconds()),
	})
}

// cookieMaxAge：浏览器把 cookie 上限压到 400 天，所以「永不过期」在浏览器那边只能是
// 400 天 + 每次打开页面重新下发（whoami 那里会刷）。
func (s *Store) cookieMaxAge() time.Duration {
	if s.cfg.TTL <= 0 || s.cfg.TTL > 400*24*time.Hour {
		return 400 * 24 * time.Hour
	}
	return s.cfg.TTL
}

func (s *Store) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/",
		SameSite: http.SameSiteStrictMode, HttpOnly: true, Secure: true, MaxAge: -1,
	})
}

/* ------------------------------------------------------------------ 小工具 */

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randToken() string {
	b := make([]byte, 32)
	must(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randID() string {
	b := make([]byte, 6)
	must(b)
	return hex.EncodeToString(b)
}

// randCode 用拒绝采样，别让字母表长度不整除 256 引入偏斜。
func randCode() string {
	out := make([]byte, 0, codeLen)
	buf := make([]byte, 1)
	max := byte(len(codeAlphabet) * (256 / len(codeAlphabet)))
	for len(out) < codeLen {
		must(buf)
		if buf[0] >= max {
			continue
		}
		out = append(out, codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	return string(out)
}

func must(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand 读不出来：" + err.Error()) // 这台机器上没有随机数，别硬撑
	}
}

// normalizeCode 容忍手输的大小写、空格和连字符，别的一律当错。
//
// 不做 O→0 / I→1 这类「纠正」：字母表里 0 1 I L O 五个字符全都没有，所以看到它们只能
// 说明用户抄错了，猜哪个都是瞎猜。
func normalizeCode(in string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(in) {
		if r == ' ' || r == '-' || r == '_' {
			continue
		}
		if !strings.ContainsRune(codeAlphabet, r) {
			return ""
		}
		b.WriteRune(r)
	}
	if b.Len() != codeLen {
		return ""
	}
	return b.String()
}

// LabelFromUA 只求「在设备列表里能认出是哪台」，不求准。
func LabelFromUA(ua string) string {
	dev := "未知设备"
	switch {
	case strings.Contains(ua, "iPad"):
		dev = "iPad"
	case strings.Contains(ua, "iPhone"):
		dev = "iPhone"
	case strings.Contains(ua, "Android"):
		dev = "Android"
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		dev = "Mac"
	case strings.Contains(ua, "Windows"):
		dev = "Windows"
	case strings.Contains(ua, "Linux"):
		dev = "Linux"
	}
	br := ""
	switch {
	case strings.Contains(ua, "GerdrApp/"): // 安卓 App 的 WebView（android/ 里给 UA 加的那一截），和手机浏览器分开
		br = "App"
	case strings.Contains(ua, "Edg"):
		br = "Edge"
	case strings.Contains(ua, "CriOS"), strings.Contains(ua, "Chrome"):
		br = "Chrome"
	case strings.Contains(ua, "Firefox"), strings.Contains(ua, "FxiOS"):
		br = "Firefox"
	case strings.Contains(ua, "Safari"):
		br = "Safari"
	}
	if br == "" {
		return dev
	}
	return dev + " · " + br
}
