package engine

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// startTestSSHServer 在进程内启动一个可用的SSH测试服务器：
// 密码认证放行；exec请求用本地sh真实执行；mode="sftp"时支持SFTP子系统
// （真实落盘本机/tmp），mode="nosftp"时拒绝SFTP子系统（触发管道兜底模式）。
func startTestSSHServer(t *testing.T, mode string) SSHConfig {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成主机密钥失败: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("构建签名器失败: %v", err)
	}

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
			go handleTestConn(c, cfg, mode)
		}
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	return SSHConfig{Host: host, Port: portStr, User: "tester", Pass: "secret"}
}

func handleTestConn(c net.Conn, cfg *ssh.ServerConfig, mode string) {
	sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unsupported channel")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go handleTestSession(ch, chReqs, mode)
	}
}

// handleTestSession 处理exec与subsystem(sftp)请求，exec用本地sh真实执行
func handleTestSession(ch ssh.Channel, reqs <-chan *ssh.Request, mode string) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "exec":
			var payload struct{ Command string }
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				req.Reply(false, nil)
				return
			}
			req.Reply(true, nil)
			exitCode := runLocal(ch, payload.Command)
			status := struct{ Status uint32 }{uint32(exitCode)}
			ch.SendRequest("exit-status", false, ssh.Marshal(status))
			return
		case "subsystem":
			var payload struct{ Name string }
			ssh.Unmarshal(req.Payload, &payload)
			if payload.Name != "sftp" || mode != "sftp" {
				req.Reply(false, nil) // 拒绝 → 客户端回退管道模式
				continue
			}
			req.Reply(true, nil)
			sv, err := sftp.NewServer(ch)
			if err != nil {
				return
			}
			sv.Serve() // 服务至客户端关闭SFTP连接
			sv.Close()
			return
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// runLocal 用本地sh执行远端命令（stdin接通道以支持 sh -s 管道模式）
func runLocal(ch ssh.Channel, command string) int {
	var cmd *exec.Cmd
	if strings.TrimSpace(command) == "sh -s" {
		cmd = exec.Command("sh", "-s")
		cmd.Stdin = ch
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

// 测试脚本：stderr输出过程日志，stdout输出纯净JSON（协议规范）
const testScript = `echo "UART1 测试开始" >&2
echo "收发100字节" >&2
echo '[{"test_name":"UART1 自发自收测试","test_item":"uart1","result":"pass","msg":"收发100字节校验成功","raw_log":"ok"},{"test_name":"ETH0 网关PING测试","test_item":"eth0","result":"fail","msg":"网关超时无响应","raw_log":"100% packet loss"}]'
`

func runEngine(t *testing.T, serverMode string) ([]TestItem, []string) {
	t.Helper()
	cfg := startTestSSHServer(t, serverMode)
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer client.Close()

	runner := NewRunner(client)
	runner.ExecTimeout = 15 * time.Second

	var mu sync.Mutex
	var logs []string
	items, err := runner.Run([]byte(testScript), func(line string) {
		mu.Lock()
		logs = append(logs, line)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Run失败: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return items, logs
}

func TestRunnerSFTPMode(t *testing.T) {
	items, logs := runEngine(t, "sftp")
	if len(items) != 2 {
		t.Fatalf("结果项数=%d, 期望2", len(items))
	}
	if items[0].Result != ResultPass || items[1].Result != ResultFail {
		t.Fatalf("结果=%q/%q, 期望pass/fail", items[0].Result, items[1].Result)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "UART1 测试开始") {
		t.Errorf("stderr日志未实时分流: %q", joined)
	}
}

func TestRunnerPipeFallback(t *testing.T) {
	items, logs := runEngine(t, "nosftp")
	if len(items) != 2 {
		t.Fatalf("结果项数=%d, 期望2", len(items))
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "SFTP不可用") {
		t.Errorf("缺少SFTP回退提示: %q", joined)
	}
	if !strings.Contains(joined, "收发100字节") {
		t.Errorf("管道模式stderr未分流: %q", joined)
	}
}

func TestRunnerLogFnPanicDoesNotKill(t *testing.T) {
	cfg := startTestSSHServer(t, "nosftp")
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer client.Close()

	runner := NewRunner(client)
	n := 0
	items, runErr := runner.Run([]byte(testScript), func(string) {
		n++
		if n == 1 {
			panic("logFn boom")
		}
	})
	if runErr != nil && len(items) == 0 {
		// 脚本仍应跑完；panic 只影响单次日志回调
		t.Fatalf("logFn panic 不应导致 Run 失败: %v", runErr)
	}
	if len(items) != 2 {
		t.Fatalf("结果项数=%d, 期望2 (logFn panic 后仍应解析 JSON)", len(items))
	}
}

func TestRunnerTimeout(t *testing.T) {
	cfg := startTestSSHServer(t, "nosftp")
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer client.Close()

	runner := NewRunner(client)
	runner.ExecTimeout = 1 * time.Second // 脚本sleep 5s，必然超时
	_, err = runner.Run([]byte("sleep 5\n"), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("期望超时错误, 实际: %v", err)
	}
}
