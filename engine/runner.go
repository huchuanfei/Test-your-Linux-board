package engine

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// 默认超时配置
const (
	DefaultDialTimeout = 10 * time.Second // SSH连接超时
	DefaultExecTimeout = 10 * time.Minute // 脚本整体执行超时
	remoteScriptPrefix = "/tmp/boardtest_"
)

// Runner 测试执行引擎：SFTP上传执行优先，无SFTP时管道灌入兜底
type Runner struct {
	client *Client

	// ExecTimeout 脚本整体执行超时，零值取DefaultExecTimeout
	ExecTimeout time.Duration
}

// NewRunner 基于已建立的SSH连接构建执行引擎
func NewRunner(c *Client) *Runner {
	return &Runner{client: c}
}

// Run 执行测试脚本并返回结构化结果。
// logFn逐行接收脚本stderr原始日志（可能从内部goroutine并发调用，
// 调用方须保证线程安全，UI侧应经 fyne.Do 派发到主线程）。
// stdout按协议仅承载JSON结果，执行完毕后统一容错解析；
// 协议约定单项失败不退出脚本，故非零退出码不影响已产出JSON的解析。
func (r *Runner) Run(script []byte, logFn func(string)) ([]TestItem, error) {
	if len(bytes.TrimSpace(script)) == 0 {
		return nil, fmt.Errorf("测试脚本内容为空")
	}
	timeout := r.ExecTimeout
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}

	// stderr统一经管道逐行分流给logFn
	pr, pw := io.Pipe()
	stderrDone := make(chan struct{})
	if logFn == nil {
		logFn = func(string) {}
	}
	go func() {
		// 扫描协程无 recover 时，logFn/扫描 panic 会直接干掉整个进程。
		// 「开始测试」独有这条路径，「连接测试」不会走到这里。
		defer close(stderrDone)
		defer func() { _ = recover() }()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			func() {
				defer func() { _ = recover() }()
				logFn(sc.Text())
			}()
		}
	}()

	var stdout bytes.Buffer
	var cmdDesc string
	var runErr error

	// 优先模式：SFTP上传脚本到/tmp执行
	if remote, err := r.uploadViaSFTP(script); err == nil {
		cmdDesc = fmt.Sprintf("SFTP模式 %s", remote)
		// 执行完顺带清理临时脚本；退出码不关心（结果以JSON为准）
		cmd := fmt.Sprintf("sh %s; rm -f %s", remote, remote)
		runErr = r.exec(cmd, nil, &stdout, pw, timeout)
	} else {
		if remote != "" { // 上传中途失败，清理半成品文件（尽力而为）
			r.bestEffortRemove(remote)
		}
		func() {
			defer func() { _ = recover() }()
			logFn(fmt.Sprintf("SFTP不可用(%v)，回退管道模式直接执行", err))
		}()
		// 兜底模式：脚本经stdin直接灌入远端sh，不在板卡落地文件
		cmdDesc = "管道模式 sh -s"
		runErr = r.exec("sh -s", bytes.NewReader(script), &stdout, pw, timeout)
	}

	pw.Close()   // 所有Session已结束，通知日志扫描协程收尾
	<-stderrDone // 等待日志行冲刷完毕

	if items, err := ParseResults(stdout.String()); err == nil {
		return items, nil
	}
	if runErr != nil {
		return nil, fmt.Errorf("脚本执行失败(%s): %w", cmdDesc, runErr)
	}
	return nil, fmt.Errorf("脚本执行完成(%s)，但stdout无可解析的JSON测试结果", cmdDesc)
}

// uploadViaSFTP 优先模式：上传脚本到板卡/tmp并加执行权限。
// 返回远端路径；SFTP环境不可用/上传失败时返回错误（触发管道兜底），
// 路径非空表示远端残留半成品文件需清理。
func (r *Runner) uploadViaSFTP(script []byte) (string, error) {
	sc, err := sftp.NewClient(r.client.ssh)
	if err != nil {
		return "", fmt.Errorf("SFTP子系统不可用: %w", err)
	}
	defer sc.Close()

	remote := fmt.Sprintf("%s%d.sh", remoteScriptPrefix, time.Now().UnixNano())
	f, err := sc.Create(remote)
	if err != nil {
		return "", fmt.Errorf("创建远端文件失败: %w", err)
	}
	if _, err := f.Write(script); err != nil {
		f.Close()
		return remote, fmt.Errorf("写入远端文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return remote, fmt.Errorf("关闭远端文件失败: %w", err)
	}
	if err := sc.Chmod(remote, 0o755); err != nil {
		return remote, fmt.Errorf("设置执行权限失败: %w", err)
	}
	return remote, nil
}

// exec 在独立Session中执行命令：stdin可选灌入脚本、stdout收集JSON、stderr分流日志。
// 超时后强制关闭Session中止远端执行。
func (r *Runner) exec(cmd string, stdin io.Reader, stdout, stderr io.Writer, timeout time.Duration) error {
	sess, err := r.client.ssh.NewSession()
	if err != nil {
		return fmt.Errorf("创建会话失败: %w", err)
	}
	defer sess.Close()

	sess.Stdout = stdout
	sess.Stderr = stderr
	if stdin != nil {
		sess.Stdin = stdin
	}
	if err := sess.Start(cmd); err != nil {
		return fmt.Errorf("启动命令失败: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("等待远端会话异常: %v", r)
			}
		}()
		done <- sess.Wait()
	}()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		if ee, ok := err.(*ssh.ExitError); ok {
			return fmt.Errorf("远端退出码%d", ee.ExitStatus())
		}
		return err
	case <-time.After(timeout):
		sess.Close() // 中止远端执行，解除Wait阻塞
		<-done
		return fmt.Errorf("执行超时(%v)", timeout)
	}
}

// bestEffortRemove 尽力删除远端文件，失败忽略
func (r *Runner) bestEffortRemove(path string) {
	sess, err := r.client.ssh.NewSession()
	if err != nil {
		return
	}
	defer sess.Close()
	_ = sess.Run("rm -f " + path)
}
