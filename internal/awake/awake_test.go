package awake

import (
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func fake(grace time.Duration) (*Keeper, *atomic.Int32) {
	var running atomic.Int32
	k := &Keeper{log: slog.New(slog.NewTextHandler(io.Discard, nil)), grace: grace}
	k.start = func() (func(), error) {
		running.Add(1)
		return func() { running.Add(-1) }, nil
	}
	return k, &running
}

func TestKeeper(t *testing.T) {
	k, running := fake(50 * time.Millisecond)

	// 开关关着：连上也不挡
	r1 := k.Hold()
	if k.Awake() {
		t.Fatal("没打开开关就挡了睡眠")
	}
	// 打开：已经连着的立刻生效
	k.SetEnabled(true)
	if !k.Awake() || running.Load() != 1 {
		t.Fatal("打开开关时已有远程连着，应该立刻挡")
	}
	r2 := k.Hold()
	r1()
	r1() // 重复调不算两次
	if !k.Awake() {
		t.Fatal("还有一个连着")
	}
	r2()
	if !k.Awake() {
		t.Fatal("刚断开，宽限期内还该醒着")
	}
	// 宽限期内又连上：倒计时作废
	r3 := k.Hold()
	time.Sleep(80 * time.Millisecond)
	if !k.Awake() {
		t.Fatal("宽限期内重连了，不该放手")
	}
	r3()
	time.Sleep(100 * time.Millisecond)
	if k.Awake() || running.Load() != 0 {
		t.Fatal("宽限期过了还没放手")
	}
	if running.Load() != 0 {
		t.Fatal("caffeinate 没停")
	}

	// 关开关立刻放手
	r4 := k.Hold()
	k.SetEnabled(false)
	if k.Awake() {
		t.Fatal("关了开关还挡着")
	}
	r4()
}

func TestManual(t *testing.T) {
	k, running := fake(time.Hour)
	// 手动时段不看自动开关
	k.SetManual(time.Now().Add(50 * time.Millisecond))
	if !k.Awake() || k.Status().ManualUntil <= 0 {
		t.Fatal("手动时段没生效")
	}
	// 改成无限期：旧的到点回调不能把它结束掉
	k.SetManual(Forever)
	time.Sleep(80 * time.Millisecond)
	if !k.Awake() || k.Status().ManualUntil != -1 {
		t.Fatal("无限期被旧的倒计时结束了")
	}
	k.SetManual(time.Time{})
	if k.Awake() || running.Load() != 0 {
		t.Fatal("结束手动时段没放手")
	}
	// 到点自己结束
	k.SetManual(time.Now().Add(30 * time.Millisecond))
	time.Sleep(80 * time.Millisecond)
	if k.Awake() || k.Status().ManualUntil != 0 {
		t.Fatal("手动时段到点没结束")
	}
	// 远程连着 + 自动开着时，结束手动时段不影响自动那段
	k.SetEnabled(true)
	r := k.Hold()
	k.SetManual(Forever)
	k.SetManual(time.Time{})
	if !k.Awake() {
		t.Fatal("还有远程连着")
	}
	r()
}

func TestLid(t *testing.T) {
	k, _ := fake(time.Hour)
	var lid atomic.Int32
	var fail, low atomic.Bool
	fail.Store(true)
	k.lidOn = func() (func(), error) {
		if fail.Load() {
			return nil, errors.New("没授权")
		}
		lid.Add(1)
		return func() { lid.Add(-1) }, nil
	}
	k.battLow = low.Load
	k.done = make(chan struct{})
	go k.watchBattery(10 * time.Millisecond)
	defer k.Close()

	// 开关开着，但没理由醒着：不动 pmset
	k.SetLid(true)
	if lid.Load() != 0 {
		t.Fatal("没在醒着就开了合盖模式")
	}
	// 醒着但没授权：记下原因
	k.SetManual(Forever)
	if st := k.Status(); st.LidOn || st.LidErr == "" {
		t.Fatalf("没授权应该报原因：%+v", st)
	}
	// 授权完 Retry
	fail.Store(false)
	k.Retry()
	if lid.Load() != 1 {
		t.Fatal("Retry 之后合盖模式没开")
	}
	// 快没电：放手；充上电：回来
	low.Store(true)
	time.Sleep(50 * time.Millisecond)
	if lid.Load() != 0 || !k.Status().LowBattery {
		t.Fatal("电量低没放手")
	}
	if !k.Awake() {
		t.Fatal("电量低只放合盖模式，闲置不睡照旧")
	}
	low.Store(false)
	time.Sleep(50 * time.Millisecond)
	if lid.Load() != 1 {
		t.Fatal("电量回来了合盖模式没回来")
	}
	// 结束手动时段：一起放手
	k.SetManual(time.Time{})
	if lid.Load() != 0 {
		t.Fatal("不醒着了合盖模式还开着")
	}
}
