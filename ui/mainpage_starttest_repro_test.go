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

	"fyne.io/fyne/v2/test"
	"golang.org/x/crypto/ssh"
)

// startExecSSHServer 启动一个支持 exec 请求的 SSH 服务器（用于复现 startTest）。
func startExecSSHServer(t *testing.T) uiTermAddr {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	signer, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveExecConn(c, cfg)
		}
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	return uiTermAddr{host: host, port: port}
}

func serveExecConn(c net.Conn, cfg *ssh.ServerConfig) {
	sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go serveExecSession(ch, chReqs)
	}
}

func serveExecSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			req.Reply(false, nil)
			return
		}
		req.Reply(true, nil)
		cmd := exec.Command("sh", "-c", payload.Command)
		cmd.Stdout = ch
		cmd.Stderr = ch.Stderr()
		cmd.Stdin = ch
		err := cmd.Run()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = 1
			}
		}
		status := struct{ Status uint32 }{uint32(code)}
		ch.SendRequest("exit-status", false, ssh.Marshal(status))
		return
	}
}

const reproScript = "echo UART1开始 >&2\necho '[{\"test_name\":\"UART1\",\"test_item\":\"uart1\",\"result\":\"pass\",\"msg\":\"ok\",\"raw_log\":\"ok\"}]'\n"

// TestMainPageStartTestRepro 复现"开始测试"闪退。
func TestMainPageStartTestRepro(t *testing.T) {
	addr := startExecSSHServer(t)
	p := newTestMainPage(t)

	p.ipEntry.SetText(addr.host)
	p.portEntry.SetText(addr.port)
	p.userEntry.SetText("tester")
	p.passEntry.SetText("secret")

	tmp, err := os.CreateTemp("", "repro_*.sh")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(reproScript); err != nil {
		t.Fatalf("写脚本失败: %v", err)
	}
	tmp.Close()
	p.scriptPath = tmp.Name()

	p.startTest()

	if !waitFor(8*time.Second, func() bool { return !p.isRunning() }) {
		t.Fatalf("测试结束后 running 状态未复位")
	}
	if len(p.items) != 1 {
		t.Fatalf("结果项数=%d, 期望1, items=%v", len(p.items), p.items)
	}
	// 强制真实渲染表格，触发 cell/header 回调（模拟真实 app 的主线程渲染）
	_ = test.RenderObjectToMarkup(p.resultTable)
}

// TestFinishRenderDirect 直接调用 finishRun 并强制渲染，验证主线程路径无 panic
func TestFinishRenderDirect(t *testing.T) {
	p := newTestMainPage(t)
	items := []engine.TestItem{
		{TestName: "UART1", TestItem: "uart1", Result: engine.ResultPass, Msg: "ok"},
		{TestName: "ETH0", TestItem: "eth0", Result: engine.ResultFail, Msg: "fail"},
	}
	// 直接调用 finishRun（模拟 fyne.Do 在主线程执行）
	p.finishRun(items, nil)
	_ = test.RenderObjectToMarkup(p.resultTable)
	if len(p.items) != 2 {
		t.Fatalf("结果项数=%d, 期望2", len(p.items))
	}
}

// TestFinishRunErrorKeepsAlive 解析/执行失败只弹错，不退出
func TestFinishRunErrorKeepsAlive(t *testing.T) {
	p := newTestMainPage(t)
	p.setRunning(true, "err-path")
	if p.btnStart.Disabled() || p.btnOpenScript.Disabled() || p.btnConnTest.Disabled() {
		t.Fatal("忙碌时不应 Disable 按钮")
	}
	p.finishRun(nil, fmt.Errorf("stdout无可解析的JSON"))
	if p.isRunning() {
		t.Fatal("失败路径 running 应复位")
	}
	if p.btnStart.Disabled() || p.btnOpenScript.Disabled() || p.btnConnTest.Disabled() {
		t.Fatal("失败路径按钮应保持启用")
	}
	_ = test.RenderObjectToMarkup(p.resultTable)
}
