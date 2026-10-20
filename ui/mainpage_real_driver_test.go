//go:build x11

package ui

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"boardtest/engine"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"golang.org/x/crypto/ssh"
)

// TestStartTestRealDriver 用真实 GLFW 驱动构建 MainPage，程序化触发 startTest，
// 复现真实 app 中 fyne.Do 回调在主线程执行（无 goroutine recover 保护）时的崩溃。
// 运行：DISPLAY=:0 go test -tags x11 ./ui/ -run TestStartTestRealDriver -v
func TestStartTestRealDriver(t *testing.T) {
	if os.Getenv("RUN_REAL_DRIVER") != "1" {
		t.Skip("跳过：需 RUN_REAL_DRIVER=1 才用真实驱动跑（避免 CI 无显示）")
	}
	addr := startExecSSHServer(t)

	a := app.New()
	w := a.NewWindow("repro-startTest")
	w.Resize(fyne.NewSize(1240, 760))

	p := newMainPage(w, nil)
	w.SetContent(p.root)
	w.Show()

	// 配置参数
	p.ipEntry.SetText(addr.host)
	p.portEntry.SetText(addr.port)
	p.userEntry.SetText("tester")
	p.passEntry.SetText("secret")
	tmp, err := os.CreateTemp("", "repro_*.sh")
	if err != nil {
		t.Fatalf("创建临时脚本失败: %v", err)
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(reproScript)
	tmp.Close()
	p.scriptPath = tmp.Name()

	// 主线程 panic 捕获（fyne.Do 回调在主线程，无 recover）
	panicCh := make(chan interface{}, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicCh <- r
			}
		}()
		// 等待窗口渲染
		time.Sleep(800 * time.Millisecond)
		// 在主线程触发 startTest（通过 fyne.Do）
		fyne.Do(func() { p.startTest() })
		// 等待测试结束
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if !p.isRunning() {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(500 * time.Millisecond) // 让 finishRun 的 dialog/refresh 完成
		fyne.Do(func() { a.Quit() })
	}()

	go func() {
		time.Sleep(20 * time.Second)
		a.Quit()
	}()

	a.Run()

	select {
	case r := <-panicCh:
		t.Fatalf("主线程捕获到 panic: %v", r)
	default:
	}

	if p.isRunning() {
		t.Fatal("测试结束后 running 仍未复位")
	}
	if len(p.items) != 1 {
		t.Fatalf("结果项数=%d, 期望1, items=%v", len(p.items), p.items)
	}
	fmt.Println("TestStartTestRealDriver 通过: items=", len(p.items))
}

// 复用 startExecSSHServer（在 mainpage_starttest_repro_test.go 中定义）。
// 为避免重复定义冲突，这里不重新定义。

var _ = exec.Command
var _ = ed25519.GenerateKey
var _ = rand.Reader
var _ = net.Listen
var _ = ssh.NewServerConn
var _ = engine.ResultPass
