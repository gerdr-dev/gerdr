// Package awake：有远程设备连着时不让这台 Mac 睡（和 Amphetamine 一个意思）。
//
// 为什么要它：Mac 闲置一会儿就睡，睡着了隧道断、agent 停，手机上远程看到的是「连不上」——
// 人在外面用手机盯 agent 的时候恰恰没人去碰电脑。
//
// 怎么做：起一个 `caffeinate -i -s -w <gerdr 的 pid>`，要醒着就留着它，不用了就杀掉。
//   - 不用 IOKit 的 IOPMAssertion：那要 cgo，整个项目不开 cgo；caffeinate 是系统自带的，效果一样。
//   - `-w <pid>`：gerdr 崩了 / 被强杀，caffeinate 自己跟着退，不会留一个让电脑永远不睡的孤儿。
//   - `-i` 挡闲置睡眠；`-s` 插着电时连「系统睡眠」也挡（合盖仍然会睡，那是硬件层的，caffeinate 管不了 ——
//     合盖不睡另有一个开关，走 pmset disablesleep，见 lid_darwin.go）。
//     不挡显示器睡眠（-d）：远程用不着屏幕亮着，熄屏省电。
//
// 「连着」= 有远程的 /ws 开着（界面开着就一直有这条）。手机锁屏 / 切后台时浏览器会把它断掉，
// 所以断了之后再撑 Grace（15 分钟）才放手：拿起手机接着看的时候电脑还醒着。
//
// 另有一个手动的时段（设置 → 实验室，手机上也能点）：无限期 / 多少分钟 / 直到几点，和 Amphetamine 的「开启新会话」
// 一个意思。它不看自动那个开关 —— 人明确说了要醒着，就醒着。
package awake

import (
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Grace：最后一个远程连接断开之后还醒着多久。
const Grace = 15 * time.Minute

type Keeper struct {
	log   *slog.Logger
	grace time.Duration
	// start 起一个让系统别睡的进程，返回停掉它的函数；测试里换成假的。nil = 这个平台做不到。
	start func() (stop func(), err error)
	// lidOn 打开「合盖也不睡」，返回关掉它的函数；lidReady 是不是已经授权过；batt 读电量和电源。测试里换成假的。
	lidOn    func() (stop func(), err error)
	lidReady func() bool
	batt     func() Battery
	done     chan struct{}

	mu      sync.Mutex
	enabled bool
	conns   int
	timer   *time.Timer // 最后一个连接断开后的倒计时
	idleAt  time.Time   // 最后一个连接断开的时间（算宽限期到几点）
	manual  time.Time   // 手动时段到几点；零值 = 没开，Forever = 无限期
	mtimer  *time.Timer // 手动时段到点
	stop    func()      // 非 nil = 正醒着
	lid     bool        // 实验室开关：合盖也不睡
	lidStop func()      // 非 nil = disablesleep 正开着
	lidErr  string      // 上次开合盖模式失败的原因
	lowBatt bool        // 用电池且电量 ≤ 20%：合盖模式放手
}

// Battery：电量和电源。Percent -1 = 没电池（台式机）。
type Battery struct {
	Percent   int  `json:"percent"`
	OnBattery bool `json:"onBattery"` // 在用电池（没插电）
	Charging  bool `json:"charging"`
}

// ReadBattery reads this machine's battery right now (GET /api/battery). Percent -1 = no battery or not macOS.
func ReadBattery() Battery { return battery() }

// low：用电池且电量 ≤ 20%，合盖模式放手的线。
func (b Battery) low() bool { return b.OnBattery && b.Percent >= 0 && b.Percent <= 20 }

// Forever：手动时段「无限期」。
var Forever = time.Unix(1<<62, 0)

// New：dir 是 gerdr 的数据目录（放合盖模式的标记文件）。
func New(log *slog.Logger, dir string) *Keeper {
	k := &Keeper{log: log, grace: Grace, done: make(chan struct{})}
	if runtime.GOOS == "darwin" {
		if p, err := exec.LookPath("caffeinate"); err == nil {
			k.start = func() (func(), error) { return caffeinate(p) }
		}
		lidCleanup(dir)
		k.lidOn = func() (func(), error) { return lidOn(dir) }
		k.lidReady, k.batt = lidReady, battery
		go k.watchBattery(time.Minute)
	}
	return k
}

// watchBattery：合盖模式开着（或因为没电放手了）时每 every 看一眼电量。合着盖子在包里耗光电比睡着糟得多。
func (k *Keeper) watchBattery(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-k.done:
			return
		case <-t.C:
		}
		k.mu.Lock()
		check := k.lidStop != nil || k.lowBatt
		k.mu.Unlock()
		if !check {
			continue
		}
		low := k.batt().low()
		k.mu.Lock()
		if low != k.lowBatt {
			k.lowBatt = low
			if low {
				k.log.Warn("awake: 电量低，合盖模式放手")
			}
			k.sync()
		}
		k.mu.Unlock()
	}
}

// SetLid：设置 → 实验室的「合盖也不睡」。只在本来就该醒着的时候生效（远程连着 / 手动时段）。
func (k *Keeper) SetLid(on bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.lid == on {
		return
	}
	k.lid, k.lidErr = on, ""
	k.sync()
}

// Retry：授权完之后再试一次合盖模式。
func (k *Keeper) Retry() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lidErr = ""
	k.sync()
}

func caffeinate(path string) (func(), error) {
	cmd := exec.Command(path, "-i", "-s", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	return func() { _ = cmd.Process.Kill() }, nil
}

// SetEnabled：设置 → 实验室的开关。关掉立刻放手；打开时如果已经有远程连着，立刻开始醒着。
func (k *Keeper) SetEnabled(on bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.enabled == on {
		return
	}
	k.enabled = on
	k.sync()
}

// Hold：一个远程连接来了。返回的函数在它断开时调（只算一次）。
func (k *Keeper) Hold() (release func()) {
	k.mu.Lock()
	k.conns++
	if k.timer != nil {
		k.timer.Stop()
		k.timer = nil
	}
	k.sync()
	k.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			k.mu.Lock()
			defer k.mu.Unlock()
			k.conns--
			if k.conns == 0 {
				var t *time.Timer
				t = time.AfterFunc(k.grace, func() {
					// 先拿锁再读 t：t 是 AfterFunc 返回之后（持锁）才赋上的
					k.mu.Lock()
					defer k.mu.Unlock()
					// 只认当前这一轮 —— 到点的回调可能正等着锁，这期间有人连上又断开、换了新的一轮，
					// 旧回调不能把新的宽限期提前结束掉。
					if k.timer == t {
						k.timer = nil
						k.sync()
					}
				})
				k.timer, k.idleAt = t, time.Now()
			}
		})
	}
}

// SetManual：手动时段到 until 为止（Forever = 无限期，零值 = 结束手动时段）。
func (k *Keeper) SetManual(until time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.mtimer != nil {
		k.mtimer.Stop()
		k.mtimer = nil
	}
	if !until.IsZero() && !until.After(time.Now()) {
		until = time.Time{}
	}
	k.manual = until
	if !until.IsZero() && until != Forever {
		var t *time.Timer
		t = time.AfterFunc(time.Until(until), func() {
			k.mu.Lock()
			defer k.mu.Unlock()
			if k.mtimer == t { // 同 expire：只认当前这一轮
				k.mtimer, k.manual = nil, time.Time{}
				k.sync()
			}
		})
		k.mtimer = t
	}
	k.sync()
}

// Awake：现在是不是在挡着睡眠。
func (k *Keeper) Awake() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.stop != nil
}

// Status：给设置页看的。时间都是 unix 毫秒，0 = 没有，-1 = 无限期。
type Status struct {
	Available   bool  `json:"available"`   // 这个平台做得到（macOS 且有 caffeinate）
	Awake       bool  `json:"awake"`       // 现在正挡着睡眠
	Auto        bool  `json:"auto"`        // 实验室开关：远程连着时自动不睡
	Remote      int   `json:"remote"`      // 现在连着的远程界面数
	GraceUntil  int64 `json:"graceUntil"`  // 远程都断了，自动的那段撑到几点
	ManualUntil int64 `json:"manualUntil"` // 手动时段到几点

	Lid        bool   `json:"lid"`        // 实验室开关：合盖也不睡
	LidReady   bool   `json:"lidReady"`   // 授权过了（sudoers 放行了 pmset disablesleep）
	LidOn      bool   `json:"lidOn"`      // 现在合盖也不睡
	LidErr     string `json:"lidErr"`     // 开不起来的原因
	LowBattery bool   `json:"lowBattery"` // 电量低，合盖模式放手了

	Battery Battery `json:"battery"` // 现在的电量和电源，设置页上标出来（合盖不睡时心里有数）
}

func (k *Keeper) Status() Status {
	ready := k.lidReady != nil && k.lidReady() // 跑一下 sudo -l / pmset，别拿着锁
	batt := Battery{Percent: -1}
	if k.batt != nil {
		batt = k.batt()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	st := Status{Available: k.start != nil, Awake: k.stop != nil, Auto: k.enabled, Remote: k.conns,
		Lid: k.lid, LidReady: ready, LidOn: k.lidStop != nil, LidErr: k.lidErr, LowBattery: k.lowBatt, Battery: batt}
	if k.timer != nil {
		st.GraceUntil = k.idleAt.Add(k.grace).UnixMilli()
	}
	switch {
	case k.manual == Forever:
		st.ManualUntil = -1
	case !k.manual.IsZero():
		st.ManualUntil = k.manual.UnixMilli()
	}
	return st
}

// Close：退出时放手（caffeinate 有 -w 兜底，这里只是别等它自己发现）。
func (k *Keeper) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.done != nil {
		select {
		case <-k.done:
		default:
			close(k.done)
		}
	}
	k.enabled, k.manual = false, time.Time{}
	k.sync()
}

// sync 让「醒着没」对上「该不该醒着」。持锁调。
func (k *Keeper) sync() {
	want := k.start != nil && (!k.manual.IsZero() || k.enabled && (k.conns > 0 || k.timer != nil))
	switch {
	case want && k.stop == nil:
		stop, err := k.start()
		if err != nil {
			k.log.Warn("awake: 起 caffeinate 失败", "err", err)
			return
		}
		k.stop = stop
		k.log.Info("awake: 开始挡着睡眠", "remote", k.conns, "manual", !k.manual.IsZero())
	case !want && k.stop != nil:
		k.stop()
		k.stop = nil
		k.log.Info("awake: 放手，电脑可以睡了")
	}
	// 合盖模式叠在上面：本来就该醒着 + 开关开着 + 不是快没电。开失败了（没授权）记下原因，不每次都重试 —— 等 Retry / 改开关。
	if !want {
		k.lidErr = "" // 下一段醒着时再试（期间可能去授权了）
	}
	wantLid := want && k.lid && k.lidOn != nil && !k.lowBatt
	switch {
	case wantLid && k.lidStop == nil && k.lidErr == "":
		stop, err := k.lidOn()
		if err != nil {
			k.lidErr = err.Error()
			k.log.Warn("awake: 合盖模式开不起来", "err", err)
			return
		}
		k.lidStop = stop
		k.log.Info("awake: 合盖也不睡")
	case !wantLid && k.lidStop != nil:
		k.lidStop()
		k.lidStop = nil
		k.log.Info("awake: 合盖模式放手")
	}
}
