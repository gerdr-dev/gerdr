package auth

import (
	"testing"
	"time"
)

func TestGateBackoffThenBlock(t *testing.T) {
	g := NewGate()
	base := time.Now()
	g.now = func() time.Time { return base }

	// 前两次不罚：手抄配对码错一位很正常
	for i := 0; i < g.FreeTries; i++ {
		g.Fail("192.168.1.77")
		if d, blocked, _ := g.Check("192.168.1.77"); d != 0 || blocked {
			t.Fatalf("第 %d 次失败后不该有惩罚（delay=%v）", i+1, d)
		}
	}
	g.Fail("192.168.1.77")
	if d, _, _ := g.Check("192.168.1.77"); d != time.Second {
		t.Errorf("第 3 次之后该退避 1s，得到 %v", d)
	}
	g.Fail("192.168.1.77")
	if d, _, _ := g.Check("192.168.1.77"); d != 2*time.Second {
		t.Errorf("退避该翻倍，得到 %v", d)
	}

	for i := 0; i < g.Threshold; i++ {
		g.Fail("192.168.1.77")
	}
	d, blocked, retry := g.Check("192.168.1.77")
	if !blocked || retry <= 0 || d != 0 {
		t.Fatalf("到阈值就该封：blocked=%v retry=%v", blocked, retry)
	}
	// 封锁到期后自动放开
	g.now = func() time.Time { return base.Add(g.BlockFor + time.Second) }
	if _, blocked, _ := g.Check("192.168.1.77"); blocked {
		t.Error("封锁期过了就该放开")
	}
}

func TestGateResetOnSuccess(t *testing.T) {
	g := NewGate()
	for i := 0; i < 5; i++ {
		g.Fail("192.168.1.77")
	}
	g.Reset("192.168.1.77")
	if d, _, _ := g.Check("192.168.1.77"); d != 0 {
		t.Errorf("配对成功后该清账，得到 delay=%v", d)
	}
}

func TestGateGlobalTrip(t *testing.T) {
	g := NewGate()
	var alerts []string
	g.Alert = func(m string) { alerts = append(alerts, m) }
	// 换源 IP 的分布式尝试：每个 IP 都没到阈值，但总数超了
	for i := 0; i < g.GlobalTrip; i++ {
		g.Fail("10.0." + itoa(i/250) + "." + itoa(i%250+1))
	}
	if !g.Locked() {
		t.Fatal("失败总数超过全局阈值就该熔断")
	}
	g.Unlock()
	if g.Locked() {
		t.Error("Unlock 之后该恢复")
	}
}

func TestGateWindowExpiry(t *testing.T) {
	g := NewGate()
	base := time.Now()
	g.now = func() time.Time { return base }
	for i := 0; i < 5; i++ {
		g.Fail("192.168.1.77")
	}
	g.now = func() time.Time { return base.Add(g.Window + time.Minute) }
	if d, _, _ := g.Check("192.168.1.77"); d != 0 {
		t.Errorf("窗口过了就既往不咎，得到 delay=%v", d)
	}
}

// 远程那条路上源地址是中继说的：没有「本机永不封」这个豁免，127.0.0.1 也照封
// （herdr-web 的 frp 时代，这个豁免把整层限速变成过空操作）。
func TestGateBlocksLoopback(t *testing.T) {
	g := NewGate()
	for i := 0; i < g.Threshold; i++ {
		g.Fail("127.0.0.1")
	}
	if _, blocked, _ := g.Check("127.0.0.1"); !blocked {
		t.Fatal("声明暴露之后 loopback 也要能被封，否则限速形同虚设")
	}
	// 逃生口：机器上 unlock 之后立刻能配对
	g.Unlock()
	if _, blocked, _ := g.Check("127.0.0.1"); blocked {
		t.Error("unlock 之后该放开")
	}
}

// 并发请求不能一起溜过闸：同一个 IP 同时只有一次在验，全局在验的有上限。
func TestGateInflight(t *testing.T) {
	g := NewGate()
	if !g.Enter("1.2.3.4") || g.Enter("1.2.3.4") {
		t.Fatal("同一个 IP 第二次 Enter 该拿不到")
	}
	if !g.Enter("5.6.7.8") {
		t.Fatal("别的 IP 不该受影响")
	}
	g.Leave("1.2.3.4")
	if !g.Enter("1.2.3.4") {
		t.Fatal("Leave 之后该能再进")
	}
	for i := 0; i < g.MaxInflight; i++ {
		if !g.Slot() {
			t.Fatalf("第 %d 个全局名额该给", i+1)
		}
	}
	if g.Slot() {
		t.Fatal("全局名额满了还给")
	}
	g.Unslot()
	if !g.Slot() {
		t.Fatal("还回去一个之后该能拿到")
	}
}
