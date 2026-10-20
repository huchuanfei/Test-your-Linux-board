package ui

import (
	"fmt"
	"image/color"
	"io"
	"sync"
	"time"

	"boardtest/engine"
	terminal "boardtest/internal/term"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/crypto/ssh"
)

// 连接状态指示配色与文案
var (
	statusConnected    = color.RGBA{R: 0x1e, G: 0x99, B: 0x40, A: 0xff} // 已连接-绿
	statusDisconnected = color.RGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xff} // 已断开-灰
	statusConnecting   = color.RGBA{R: 0xd9, G: 0x9a, B: 0x1e, A: 0xff} // 连接中-黄
)

const (
	textConnected    = "已连接"
	textDisconnected = "未连接：请先在测试主页进行连接测试"
	textConnecting   = "正在打开终端会话"
	textShellEnded   = "会话已结束"

	// 初始PTY尺寸：首次Config回调（终端控件完成布局）前的占位值
	initTermRows = 24
	initTermCols = 80
)

// eofHintReader 包装远端输出流：读到EOF时向终端注入红色断开提示后再结束，
// 满足"网络断开/远端关闭时终端显示明确提示"需求。
type eofHintReader struct {
	r    io.Reader
	done bool
}

func (h *eofHintReader) Read(p []byte) (int, error) {
	if h.done {
		return 0, io.EOF
	}
	n, err := h.r.Read(p)
	if err != nil && !h.done {
		// 红色ANSI文本：\x1b[31m 红色 \x1b[0m 复位
		hint := []byte("\r\n\x1b[31m--- 连接已断开 ---\x1b[0m\r\n")
		if len(p) >= len(hint) {
			copy(p, hint)
			h.done = true
			return len(hint), nil
		}
	}
	return n, err
}

// termSession SSH交互终端会话资源（只关 Session，不关共享 Client）
type termSession struct {
	session *ssh.Session
	cfgCh   chan terminal.Config // 终端尺寸变化通知（RemoveListener时关闭）
}

// close 释放本页 PTY 会话（幂等），不关闭页面间共享的 SSH 连接
func (s *termSession) close() {
	if s == nil {
		return
	}
	if s.session != nil {
		s.session.Close()
	}
}

// TerminalPage SSH终端对话页（复用测试主页已建立的连接）
type TerminalPage struct {
	win fyne.Window
	hub *engine.Hub

	term *terminal.Terminal

	btnClear    *widget.Button
	statusDot   *canvas.Circle
	statusLabel *widget.Label

	root fyne.CanvasObject // 页面根对象

	// 会话状态（Hub回调与终端退出协程并发访问）
	mu   sync.Mutex
	sess *termSession
}

// NewTerminalPage 构建SSH终端对话页
func NewTerminalPage(w fyne.Window, hub *engine.Hub) fyne.CanvasObject {
	return newTerminalPage(w, hub).root
}

// newTerminalPage 构建终端页对象（同包测试可直接访问内部状态）
func newTerminalPage(w fyne.Window, hub *engine.Hub) *TerminalPage {
	if hub == nil {
		hub = engine.NewHub()
	}
	p := &TerminalPage{win: w, hub: hub}

	// 终端仿真组件（完整键盘直通/ANSI渲染/回滚缓冲）
	p.term = terminal.New()

	p.btnClear = newGreenButton("清屏", p.clearScreen)

	p.statusDot = &canvas.Circle{}
	p.statusDot.Resize(fyne.NewSize(14, 14))
	dotFixed := container.NewGridWrap(fyne.NewSize(18, 18), p.statusDot)
	p.statusLabel = widget.NewLabel(textDisconnected)
	p.setStatus(statusDisconnected, textDisconnected)

	toolbar := container.NewHBox(
		dotFixed, p.statusLabel,
		p.btnClear,
	)

	termBg := canvas.NewRectangle(color.Black)
	p.root = container.NewBorder(toolbar, nil, nil, nil, container.NewStack(termBg, p.term))

	hub.Subscribe(func(ok bool) {
		fyne.Do(func() {
			// Hub 回调经 fyne.Do 跑在 GLFW 主循环，无 recover；挂 PTY 失败不能闪退。
			defer func() { _ = recover() }()
			if ok {
				p.attach()
			} else {
				p.detach()
			}
		})
	})
	return p
}

// attach 使用共享连接打开交互式 PTY（不 Dial）
func (p *TerminalPage) attach() {
	defer func() { _ = recover() }()
	if p.hub == nil || !p.hub.Connected() {
		p.setStatus(statusDisconnected, textDisconnected)
		return
	}
	p.mu.Lock()
	busy := p.sess != nil
	p.mu.Unlock()
	if busy {
		return
	}
	client := p.hub.Client()
	if client == nil {
		p.setStatus(statusDisconnected, textDisconnected)
		return
	}
	if p.term != nil {
		p.term.EnsureReady()
	}
	p.setStatus(statusConnecting, textConnecting)
	go p.runPTY(client)
}

// detach 关闭本页 PTY，共享连接由 Hub 负责
func (p *TerminalPage) detach() {
	defer func() { _ = recover() }()
	p.mu.Lock()
	ts := p.sess
	p.mu.Unlock()
	if ts != nil {
		ts.close()
		return
	}
	p.setStatus(statusDisconnected, textDisconnected)
}

// runPTY 在已有 SSH Client 上开 shell（失败只提示，不关共享连接、不闪退）
func (p *TerminalPage) runPTY(client *engine.Client) {
	fail := func(format string, args ...any) {
		msg := fmt.Errorf(format, args...)
		fyne.Do(func() {
			defer func() { _ = recover() }()
			p.setStatus(statusDisconnected, textDisconnected)
			if p.win != nil {
				dialog.ShowError(msg, p.win)
			}
		})
	}

	defer func() {
		if r := recover(); r != nil {
			fail("终端会话异常: %v", r)
		}
	}()

	if client == nil {
		fail("SSH连接未建立")
		return
	}
	sess, err := client.NewSession()
	if err != nil {
		fail("创建会话失败: %w", err)
		return
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:   1,
		ssh.ICANON: 1,
		ssh.ISIG:   1,
	}
	if err := sess.RequestPty("xterm-256color", initTermRows, initTermCols, modes); err != nil {
		sess.Close()
		fail("当前板卡不支持交互终端: %w", err)
		return
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		fail("获取stdin失败: %w", err)
		return
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		fail("获取stdout失败: %w", err)
		return
	}
	if err := sess.Shell(); err != nil {
		sess.Close()
		fail("启动Shell失败: %w", err)
		return
	}

	cfgCh := make(chan terminal.Config)
	p.term.AddListener(cfgCh)
	go func() {
		for cfg := range cfgCh {
			if cfg.Rows > 0 && cfg.Columns > 0 {
				_ = sess.WindowChange(int(cfg.Rows), int(cfg.Columns))
			}
		}
	}()

	ts := &termSession{session: sess, cfgCh: cfgCh}
	p.mu.Lock()
	p.sess = ts
	p.mu.Unlock()
	fyne.Do(func() {
		defer func() { _ = recover() }()
		p.setStatus(statusConnected, textConnected)
	})

	started := time.Now()
	_ = p.term.RunWithConnection(stdin, &eofHintReader{r: stdout})
	tooQuick := time.Since(started) < 200*time.Millisecond

	p.term.RemoveListener(cfgCh)
	ts.close()

	p.mu.Lock()
	mine := p.sess == ts
	if mine {
		p.sess = nil
	}
	p.mu.Unlock()
	if !mine {
		return
	}
	fyne.Do(func() {
		defer func() { _ = recover() }()
		if p.hub != nil && p.hub.Connected() {
			p.setStatus(statusDisconnected, textShellEnded)
			if tooQuick {
				return // 未真正跑起来，避免立刻再 attach 打死主循环
			}
			p.attach()
			return
		}
		p.setStatus(statusDisconnected, textDisconnected)
	})
}

// clearScreen 清屏：向远端Shell发送Ctrl+L；未连接时安全空操作
func (p *TerminalPage) clearScreen() {
	if p.term == nil {
		return
	}
	_, _ = p.term.Write([]byte{0x0c})
}

// setStatus 更新连接状态指示（颜色+文字），须在主线程调用
func (p *TerminalPage) setStatus(c color.Color, text string) {
	if p.statusDot != nil {
		p.statusDot.FillColor = c
		p.statusDot.Refresh()
	}
	if p.statusLabel != nil {
		p.statusLabel.SetText(text)
	}
}
