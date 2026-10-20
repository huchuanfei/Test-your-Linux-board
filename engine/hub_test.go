package engine

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestHubSetAndClose(t *testing.T) {
	cfg := startTestSSHServer(t, "nosftp")
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}

	h := NewHub()
	var last atomic.Bool
	h.Subscribe(func(ok bool) { last.Store(ok) })
	if last.Load() {
		t.Fatal("空 Hub 不应报告已连接")
	}

	h.Set(client)
	if !h.Connected() || h.Client() != client {
		t.Fatal("Set 后应持有该连接")
	}
	if !waitBool(time.Second, func() bool { return last.Load() }) {
		t.Fatal("订阅者未收到已连接")
	}

	h.Close()
	if h.Connected() {
		t.Fatal("Close 后应断开")
	}
	if !waitBool(time.Second, func() bool { return !last.Load() }) {
		t.Fatal("订阅者未收到断开")
	}
}

func TestHubReplaceNotifiesAndDropsOld(t *testing.T) {
	cfg := startTestSSHServer(t, "nosftp")
	c1, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial1失败: %v", err)
	}
	c2, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial2失败: %v", err)
	}

	h := NewHub()
	h.Set(c1)
	h.Set(c2)
	if h.Client() != c2 {
		t.Fatal("应替换为新连接")
	}
	// 旧连接应已关闭：再 NewSession 失败
	if _, err := c1.NewSession(); err == nil {
		t.Fatal("旧连接关闭后 NewSession 应失败")
	}
	sess, err := c2.NewSession()
	if err != nil {
		t.Fatalf("新连接应仍可用: %v", err)
	}
	sess.Close()
	h.Close()
}

func TestHubWatchClearsOnRemoteClose(t *testing.T) {
	cfg := startTestSSHServer(t, "nosftp")
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	h := NewHub()
	h.Set(client)
	_ = client.Close() // 对端/本端关闭触发 Wait
	if !waitBool(3*time.Second, func() bool { return !h.Connected() }) {
		t.Fatal("底层断开后 Hub 应清空")
	}
}

func TestHubNotifyIsolatesPanickingListener(t *testing.T) {
	cfg := startTestSSHServer(t, "nosftp")
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	h := NewHub()
	var okN int32
	h.Subscribe(func(bool) { panic("subscriber boom") })
	h.Subscribe(func(connected bool) {
		if connected {
			atomic.AddInt32(&okN, 1)
		}
	})
	h.Set(client)
	if !waitBool(time.Second, func() bool { return atomic.LoadInt32(&okN) == 1 }) {
		t.Fatal("一个订阅者 panic 不应阻止其他订阅者收到已连接")
	}
	if !h.Connected() {
		t.Fatal("订阅者 panic 不得回滚已成功的连接")
	}
	h.Close()
}

func waitBool(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(15 * time.Millisecond)
	}
	return cond()
}
