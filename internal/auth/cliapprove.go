package auth

import (
	"crypto/subtle"
	"errors"
	"time"
)

/* ------------------------------------------- App 登录：在浏览器里用 passkey 批准 */

// 安卓 App 优先在自己的 WebView 里刷 passkey（docs/dev/ANDROID.md「passkey」）；做不到的手机（WebView 太旧、
// 没有凭据提供方）走这条退路，照搬 herdr-web 的 device flow（它的 internal/auth/cliapprove.go）：
//
//	App    POST /api/remote/cli/start → 拿到一个**显示码**（给人看）和一个**轮询令牌**（自己留着）
//	人     在一个登录着的浏览器里**手输**显示码 → 看一眼是哪台设备、从哪个 IP 来的 →
//	       批准，当场刷一次 passkey（远程那条路上）；在跑 gerdr 的电脑上（本机口）批准不用刷
//	App    拿轮询令牌来问，批准了就拿走凭据（新设备）/ 继续用原来那份（到期重验）
//
// 为什么第一次登录也能走这条：远程批准每次都要**当场**过一把 passkey，和「用 passkey 在一台新浏览器上登录」
// 是同一件事（那条路本来就签发独立的新设备）。本机口上批准不要 passkey：本机口凭令牌就能出配对码
// （/api/remote/pair-code），批准一个请求给出的能力和出一个码一样，没有多给。
//
// 剩下的风险是**钓鱼**：谁都能发起一个请求，拿着显示码去骗人「帮我输一下」。挡它的是：显示码必须手输
// （不给带码的链接）、批准页摊出发起方的 IP 和设备、发起方就是这台机器本身时显式警告（CLIRequest.Local）。

// CLITTL 一个请求等人批准的时长。
const CLITTL = 5 * time.Minute

// cliMax：同时挂着的请求上限。发起那一步不要求认证，不设上限就是一个往内存里无限塞东西的口。
const cliMax = 16

var (
	ErrCLIUnknown = errors.New("没有这个请求，或者已经过期了（5 分钟）")
	ErrCLIBusy    = errors.New("同时等批准的请求太多了，过几分钟再试")
)

// CLIRequest 是一个等着被批准的登录。
type CLIRequest struct {
	ID      string    `json:"id"`
	Code    string    `json:"code"`   // 显示码：给人看、手输进浏览器
	Label   string    `json:"label"`  // 从 UA 猜的（「Android · App」）
	IP      string    `json:"ip"`     // 发起方的 IP
	Local   bool      `json:"local"`  // 发起方就是这台机器本身
	Device  string    `json:"device"` // 非空 = 给这台已有设备重验；空 = 新设备
	DevName string    `json:"deviceLabel,omitempty"`
	Expires time.Time `json:"expires"`

	poll     string // 轮询令牌：**只有发起方**拿得到。知道显示码（肩窥 / 钓鱼）不够领走凭据
	ua       string
	approved bool
	denied   bool
}

// StartCLI 挂一个请求。device 非空时是给那台设备重验（调用方已经用它的凭据认过了）。
func (s *Store) StartCLI(ua, ip string, local bool, device string) (CLIRequest, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.gcCLILocked(now)
	if len(s.cli) >= cliMax {
		return CLIRequest{}, "", ErrCLIBusy
	}
	r := &CLIRequest{
		ID: randID(), Code: randCode(), Label: LabelFromUA(ua), IP: ip, Local: local,
		Device: device, Expires: now.Add(CLITTL), poll: randToken(), ua: ua,
	}
	if device != "" {
		for _, d := range s.devs {
			if d.ID == device {
				r.DevName = d.Label
			}
		}
	}
	s.cli = append(s.cli, r)
	return *r, r.poll, nil
}

func (s *Store) gcCLILocked(now time.Time) {
	keep := s.cli[:0]
	for _, r := range s.cli {
		if now.Before(r.Expires) {
			keep = append(keep, r)
		}
	}
	s.cli = keep
}

// byCodeLocked 按显示码找。常数时间比对，同配对码那条理由。
func (s *Store) byCodeLocked(code string) *CLIRequest {
	code = normalizeCode(code)
	if code == "" {
		return nil
	}
	now := s.now()
	var hit *CLIRequest
	for _, r := range s.cli {
		if subtle.ConstantTimeCompare([]byte(r.Code), []byte(code)) == 1 && now.Before(r.Expires) && !r.denied {
			hit = r
		}
	}
	return hit
}

// PeekCLI 给批准页看「这是谁」。
func (s *Store) PeekCLI(code string) (CLIRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.byCodeLocked(code)
	if r == nil {
		return CLIRequest{}, ErrCLIUnknown
	}
	return *r, nil
}

// ApproveCLI 批准。调用方负责确认批准的人有这个资格（远程：刚过了一把 passkey；本机：令牌）。
//
// 重验那种当场续上（VerifiedAt）；新设备那种**等发起方来领时才签** —— 没人来领就什么都不留下。
func (s *Store) ApproveCLI(code string) (CLIRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.byCodeLocked(code)
	if r == nil {
		return CLIRequest{}, ErrCLIUnknown
	}
	if r.Device != "" {
		ok := false
		for _, d := range s.devs {
			if d.ID == r.Device && !d.Expired(s.now()) {
				d.VerifiedAt = s.now()
				ok = true
			}
		}
		if !ok {
			return CLIRequest{}, errors.New("要重验的那台设备已经被撤销或过期了")
		}
		s.flushLocked()
	}
	r.approved = true
	return *r, nil
}

// DenyCLI 拒绝。之后发起方来问会得到 denied，显示码也不再认。
func (s *Store) DenyCLI(code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.byCodeLocked(code)
	if r == nil {
		return ErrCLIUnknown
	}
	r.denied = true
	return nil
}

// CLIPoll 是发起方来问的结果。
type CLIPoll struct {
	State  string // pending | approved | denied
	Device *Device
	Token  string // 只有新设备那种、批准之后才有
}

// PollCLI 发起方拿轮询令牌来问。批准了的**领一次就删**。
func (s *Store) PollCLI(id, poll string) (CLIPoll, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.gcCLILocked(now)
	for i, r := range s.cli {
		if r.ID != id || subtle.ConstantTimeCompare([]byte(r.poll), []byte(poll)) != 1 {
			continue
		}
		switch {
		case r.denied:
			s.cli = append(s.cli[:i], s.cli[i+1:]...)
			return CLIPoll{State: "denied"}, nil
		case !r.approved:
			return CLIPoll{State: "pending"}, nil
		}
		s.cli = append(s.cli[:i], s.cli[i+1:]...)
		if r.Device != "" {
			for _, d := range s.devs {
				if d.ID == r.Device {
					c := *d
					return CLIPoll{State: "approved", Device: &c}, nil
				}
			}
			return CLIPoll{}, errors.New("那台设备已经被撤销了")
		}
		token := randToken()
		d := &Device{
			ID: randID(), Label: LabelFromUA(r.ua), Hash: hashToken(token),
			Created: now, LastSeen: now, LastIP: r.IP,
			// 在场证明是批准那一刻给的。PasskeyAt 不写：刷 passkey 的是批准的那台，不是这台 ——
			// step-up 那道门（再注册一把 passkey）对它照旧关着
			VerifiedAt: now,
		}
		if s.cfg.TTL > 0 {
			d.Expires = now.Add(s.cfg.TTL)
		}
		s.devs = append(s.devs, d)
		s.flushLocked()
		c := *d
		return CLIPoll{State: "approved", Device: &c, Token: token}, nil
	}
	return CLIPoll{}, ErrCLIUnknown
}
