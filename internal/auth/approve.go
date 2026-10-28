package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// Approvals：「到电脑上批准」—— 已有 passkey 之后再加一把要先 step-up（StepUpNeeded），而 step-up 原本只能用
// 已有的 passkey 做。新手机上一把都没有，系统只会弹「未发现任何通行密钥」，人就被卡死了。
//
// 这条路让坐在机器前的人替这台设备担保：远程设备发起请求，本机设置页看到请求 + 核对码，点批准，
// 那台设备拿到一次 step-up（Store.GrantStepUp）。强度和出配对码同一档 —— 都是「人在机器前」。
// 核对码是给人对一眼的：同时有两台设备在请求时，别批错了那台。
//
// 只在内存里：重启就作废，本来就只活几分钟。
type Approvals struct {
	mu   sync.Mutex
	reqs map[string]*Approval
	now  func() time.Time
}

// ApprovalTTL：够人走回电脑前点一下。
const ApprovalTTL = 3 * time.Minute

type Approval struct {
	ID       string    `json:"id"`
	DeviceID string    `json:"deviceId"`
	Label    string    `json:"label"`
	Code     string    `json:"code"`
	Expires  time.Time `json:"expires"`
	Approved bool      `json:"approved"`
}

func NewApprovals() *Approvals {
	return &Approvals{reqs: map[string]*Approval{}, now: time.Now}
}

// Request 给这台设备开一个请求；它之前那个作废（一台设备同时只挂一个）。
func (a *Approvals) Request(deviceID, label string) Approval {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gcLocked()
	for id, r := range a.reqs {
		if r.DeviceID == deviceID {
			delete(a.reqs, id)
		}
	}
	b := make([]byte, 16)
	must(b)
	n, _ := rand.Int(rand.Reader, big.NewInt(10000))
	r := &Approval{ID: hex.EncodeToString(b), DeviceID: deviceID, Label: label,
		Code: fmt.Sprintf("%04d", n.Int64()), Expires: a.now().Add(ApprovalTTL)}
	a.reqs[r.ID] = r
	return *r
}

// State 给发起的那台设备查（只认自己的请求）："pending" / "approved" / "expired"。
func (a *Approvals) State(id, deviceID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.reqs[id]
	switch {
	case r == nil || r.DeviceID != deviceID || a.now().After(r.Expires):
		return "expired"
	case r.Approved:
		return "approved"
	}
	return "pending"
}

// Pending 给本机设置页列出来。
func (a *Approvals) Pending() []Approval {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gcLocked()
	out := []Approval{}
	for _, r := range a.reqs {
		if !r.Approved {
			out = append(out, *r)
		}
	}
	return out
}

// Decide 本机批准 / 拒绝。批准返回那台设备的 ID（调用方给它 GrantStepUp）。
func (a *Approvals) Decide(id string, ok bool) (deviceID string, found bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.reqs[id]
	if r == nil || r.Approved || a.now().After(r.Expires) {
		return "", false
	}
	if !ok {
		delete(a.reqs, id)
		return "", true
	}
	r.Approved = true
	return r.DeviceID, true
}

func (a *Approvals) gcLocked() {
	now := a.now()
	for id, r := range a.reqs {
		if now.After(r.Expires) {
			delete(a.reqs, id)
		}
	}
}
