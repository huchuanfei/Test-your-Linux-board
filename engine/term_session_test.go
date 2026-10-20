package engine

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

// osPty 本地PTY上跑真实sh（模拟板卡端交互Shell）
type osPty struct {
	tty *os.File
	cmd *exec.Cmd
}

func openPty() (*osPty, error) {
	cmd := exec.Command("sh")
	tty, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &osPty{tty: tty, cmd: cmd}, nil
}

func (p *osPty) resize(rows, cols uint32) {
	_ = pty.Setsize(p.tty, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// ptyServerSession SSH终端页会话协议的进程内测试服务器：
// 支持 pty-req / shell / window-change 请求，shell经本地PTY跑真实sh，
// 完整复现嵌入式板卡交互终端行为（与termpage.connect的调用序列一致）。
type ptyServer struct {
	mu      chan struct{} // shell退出信号
	gotPty  bool
	gotWin  bool
	winRows int
	winCols int
}

func startPTYTestSSHServer(t *testing.T) SSHConfig {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成主机密钥失败: %v", err)
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

	srv := &ptyServer{mu: make(chan struct{})}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go servePTYConn(c, cfg, srv)
		}
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	return SSHConfig{Host: host, Port: port, User: "tester", Pass: "secret"}
}

func servePTYConn(c net.Conn, cfg *ssh.ServerConfig, srv *ptyServer) {
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
		srv.serveSession(ch, chReqs)
	}
}

// serveSession 按termpage.connect相同序列处理终端会话请求
func (srv *ptyServer) serveSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	var ptyReq struct { // RFC 4254 6.2 pty-req负载（Term后面）
		Term  string
		Cols  uint32
		Rows  uint32
		X     uint32
		Y     uint32
		Modes string
	}
	var ptyIO *osPty

	for req := range reqs {
		switch req.Type {
		case "pty-req":
			if err := ssh.Unmarshal(req.Payload, &ptyReq); err != nil {
				req.Reply(false, nil)
				return
			}
			srv.gotPty = true
			req.Reply(true, nil)
		case "shell":
			if !srv.gotPty {
				req.Reply(false, nil) // 无PTY时拒绝（真实板卡行为）
				continue
			}
			req.Reply(true, nil)
			p, err := openPty()
			if err != nil {
				ch.Close()
				return
			}
			ptyIO = p
			// 双向桥接：SSH通道 ↔ 本地PTY（真实sh进程）
			go io.Copy(ch, p.tty)
			go func() {
				io.Copy(p.tty, ch)
				p.tty.Close()
				p.cmd.Wait()
				ch.Close()
				close(srv.mu)
			}()
		case "window-change":
			var wc struct { // RFC 4254 6.7
				Cols uint32
				Rows uint32
				X    uint32
				Y    uint32
			}
			if err := ssh.Unmarshal(req.Payload, &wc); err != nil {
				req.Reply(false, nil)
				continue
			}
			srv.winRows, srv.winCols = int(wc.Rows), int(wc.Cols)
			srv.gotWin = true
			if ptyIO != nil {
				ptyIO.resize(wc.Rows, wc.Cols)
			}
			req.Reply(true, nil)
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// TestTerminalSessionProtocol 端到端验证SSH终端页会话协议：
// NewSession → RequestPty → Shell → 命令交互 → WindowChange → 断开清理
func TestTerminalSessionProtocol(t *testing.T) {
	cfg := startPTYTestSSHServer(t)
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer client.Close()

	// 与termpage.connect相同的会话序列
	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession失败: %v", err)
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.ICANON: 1, ssh.ISIG: 1}
	if err := sess.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		t.Fatalf("RequestPty失败: %v", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe失败: %v", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe失败: %v", err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatalf("Shell失败: %v", err)
	}

	// 交互命令：验证键盘输入直通远端Shell并回显
	out := readUntil(t, stdout, func(s string) bool { return strings.Contains(s, "PTY_OK_42") },
		func() { stdin.Write([]byte("echo PTY_OK_$((40+2))\n")) })

	// 尺寸同步：WindowChange后远端stty应报告新尺寸
	_ = sess.WindowChange(30, 100)
	readUntil(t, stdout, func(s string) bool { return strings.Contains(s, "30 100") },
		func() { stdin.Write([]byte("stty size\n")) })
	_ = out

	// 断开：会话与连接清理（触发终端读循环EOF路径）
	if err := sess.Close(); err != nil {
		t.Fatalf("sess.Close失败: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("client.Close失败: %v", err)
	}
}

// readUntil 发送触发命令后循环读取远端输出，直到check通过或超时
func readUntil(t *testing.T, r io.Reader, check func(string) bool, trigger func()) string {
	t.Helper()
	trigger()
	var acc strings.Builder
	buf := make([]byte, 4096)
	deadline := time.After(8 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("读取超时，已收: %q", acc.String())
		default:
		}
		n, err := r.Read(buf)
		if n > 0 {
			acc.Write(buf[:n])
			if check(acc.String()) {
				return acc.String()
			}
		}
		if err != nil {
			t.Fatalf("读取错误: %v, 已收: %q", err, acc.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
