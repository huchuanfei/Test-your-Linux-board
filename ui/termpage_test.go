package ui

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"boardtest/engine"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

// waitFor 轮询等待条件成立，超时返回最后状态描述
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(15 * time.Millisecond)
	}
	return cond()
}

// TestEOFHintReader 验证远端流EOF时向终端注入红色断开提示
func TestEOFHintReader(t *testing.T) {
	cases := []struct {
		name   string
		source io.Reader
	}{
		{"立即EOF", strings.NewReader("")},
		{"先有输出再EOF", strings.NewReader("shell prompt output")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &eofHintReader{r: c.source}
			var acc strings.Builder

			buf := make([]byte, 4096)
			for {
				n, err := r.Read(buf)
				acc.Write(buf[:n])
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("意外错误: %v", err)
				}
			}
			if n, err := r.Read(buf); n != 0 || err != io.EOF {
				t.Fatalf("重复读应返回EOF, 实际n=%d err=%v", n, err)
			}

			got := acc.String()
			if !strings.Contains(got, "--- 连接已断开 ---") {
				t.Errorf("缺少断开提示: %q", got)
			}
			if !strings.Contains(got, "\x1b[31m") {
				t.Errorf("提示非红色ANSI: %q", got)
			}
		})
	}
}

func newTestTermPage(t *testing.T, hub *engine.Hub) *TerminalPage {
	t.Helper()
	testApp := test.NewApp()
	win := testApp.NewWindow("")
	if hub == nil {
		hub = engine.NewHub()
	}
	p := newTerminalPage(win, hub)
	win.SetContent(p.root)
	win.Resize(fyne.NewSize(900, 600))
	return p
}

// TestTerminalPageUnconnectedSafe 未连接打开终端页：提示未连接、清屏不崩溃
func TestTerminalPageUnconnectedSafe(t *testing.T) {
	p := newTestTermPage(t, nil)
	if p.statusLabel.Text != textDisconnected {
		t.Fatalf("未连接文案=%q", p.statusLabel.Text)
	}
	p.clearScreen()
	p.detach()
	p.mu.Lock()
	sess := p.sess
	p.mu.Unlock()
	if sess != nil {
		t.Fatal("未连接不应有会话")
	}
}

// TestTerminalPageUsesSharedHub 主页式 Hub.Set 后终端复用该连接；Hub.Close 后会话清理
func TestTerminalPageUsesSharedHub(t *testing.T) {
	addr := startUITermSSHServer(t)
	cfg := engine.SSHConfig{Host: addr.host, Port: addr.port, User: "tester", Pass: "secret"}
	client, err := engine.Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}

	hub := engine.NewHub()
	p := newTestTermPage(t, hub)
	hub.Set(client)

	if !waitFor(5*time.Second, func() bool { return p.statusLabel.Text == textConnected }) {
		t.Fatalf("未进入已连接状态, 当前: %q", p.statusLabel.Text)
	}

	hub.Close()
	if !waitFor(5*time.Second, func() bool {
		return p.statusLabel.Text == textDisconnected || p.statusLabel.Text == textShellEnded
	}) {
		t.Fatalf("断开后状态未复位, 当前: %q", p.statusLabel.Text)
	}
	if !waitFor(2*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.sess == nil
	}) {
		t.Fatal("断开后会话应已清理")
	}
}

// TestTerminalPageDoesNotOwnConnectControls 不再提供独立地址/连接按钮
func TestTerminalPageDoesNotOwnConnectControls(t *testing.T) {
	p := newTestTermPage(t, nil)
	if p.btnClear == nil {
		t.Fatal("应保留清屏按钮")
	}
	if p.btnClear.Text != "清屏" {
		t.Fatalf("清屏按钮文案=%q", p.btnClear.Text)
	}
}

// ===== 进程内PTY SSH测试服务器（ui测试专用精简版）=====

type uiTermAddr struct {
	host, port string
}

func startUITermSSHServer(t *testing.T) uiTermAddr {
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

	var closed atomic.Bool
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveUITermConn(c, cfg, &closed)
		}
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	return uiTermAddr{host: host, port: port}
}

func serveUITermConn(c net.Conn, cfg *ssh.ServerConfig, closed *atomic.Bool) {
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
		go serveUITermSession(ch, chReqs)
	}
}

func serveUITermSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	var gotPty bool
	var closed atomic.Bool
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			gotPty = true
			req.Reply(true, nil)
		case "shell":
			if !gotPty {
				req.Reply(false, nil)
				continue
			}
			req.Reply(true, nil)
			cmd := exec.Command("sh")
			tty, err := pty.Start(cmd)
			if err != nil {
				ch.Close()
				return
			}
			go io.Copy(ch, tty)
			go func() {
				io.Copy(tty, ch)
				tty.Close()
				cmd.Wait()
				if closed.CompareAndSwap(false, true) {
					ch.Close()
				}
			}()
		case "window-change":
			req.Reply(true, nil)
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}
