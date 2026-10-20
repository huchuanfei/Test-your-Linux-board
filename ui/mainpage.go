package ui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"boardtest/engine"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

var resultHeaders = []string{"序号", "测试名称", "测试ID", "结果", "简要信息"}

// 表格显式配色（不依赖主题，保证对比度：白底黑字，通过项结果列绿字，未通过项结果列红字）
var (
	rowBg         = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} // 单元格白底
	headerBg      = color.NRGBA{R: 0xe3, G: 0xe3, B: 0xe3, A: 0xff} // 表头浅灰底
	textColor     = color.NRGBA{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff} // 正常黑字
	passTextColor = color.NRGBA{R: 0x1b, G: 0x7a, B: 0x32, A: 0xff} // 通过项结果列绿字（白底可读）
	failTextColor = color.NRGBA{R: 0xb7, G: 0x1c, B: 0x1c, A: 0xff} // 未通过项结果列红字
	frameColor    = color.NRGBA{R: 0x8a, G: 0x8a, B: 0x8a, A: 0xff} // 区域边框灰
)

// isPassResult 判断结果列为通过态（含规范化后的 pass，以及 PASS/通过 等同义词）。
func isPassResult(r string) bool {
	s := strings.ToLower(strings.TrimSpace(r))
	switch s {
	case engine.ResultPass, "success", "ok", "true", "通过":
		return true
	}
	return false
}

// isFailResult 判断结果列为不合格（与结果列红字一致：非通过即不合格）。
// 空结果不计入：表格空单元格虽走红字分支，但尚未产出判定，不当作不合格项。
func isFailResult(r string) bool {
	if strings.TrimSpace(r) == "" {
		return false
	}
	return !isPassResult(r)
}

// countFailResults 统计不合格项数（空结果不计）。
func countFailResults(items []engine.TestItem) int {
	n := 0
	for _, it := range items {
		if isFailResult(it.Result) {
			n++
		}
	}
	return n
}

// MainPage 测试主页（顶部板卡配置区 + 原始日志/测试结果左右并列）
type MainPage struct {
	win fyne.Window
	hub *engine.Hub // 与终端/文件传输页共用的 SSH 连接

	// 顶部板卡配置区（板卡差异由所选测试脚本区分，无需型号选择）
	ipEntry   *widget.Entry
	portEntry *widget.Entry
	userEntry *widget.Entry
	passEntry *widget.Entry

	// 测试脚本：直接打开本地.sh文件，不提供粘贴编辑
	scriptPath      string        // 已选脚本路径
	scriptPathEntry *widget.Entry // 只读：完整路径+文件名

	// 运行状态与控件（忙碌靠 running 防重入，不 Disable，以免 Fyne 把按钮变灰）
	running       bool
	mu            sync.Mutex // 保护running（按钮回调与执行完成回调并发）
	btnOpenScript *widget.Button
	btnConnTest   *widget.Button // 文案「连接」/「断开」，随 Hub 是否已连接切换
	btnStart      *widget.Button
	btnClearLog   *widget.Button // 原始日志标题行右上角

	// 结果表格（动态数据，由测试引擎填充）
	items         []engine.TestItem
	resultTable   *widget.Table
	failCountText *canvas.Text // 「测试结果:」旁的不合格项数（0绿 / >0红）

	// 原始日志区：多行只读 Entry（可拖选 + Ctrl+C 复制）。
	// 只读靠 OnChanged 回滚（guard: logUpdating），不用 Disable——禁用态无法鼠标拖选。
	// 自动滚底：SetText 后把光标移到末行再 Refresh。
	logEntry    *widget.Entry
	logBuf      string
	logUpdating bool

	// 页面根对象
	root fyne.CanvasObject
}

// NewMainPage 构建测试主页
func NewMainPage(w fyne.Window, hub *engine.Hub) fyne.CanvasObject {
	return newMainPage(w, hub).root
}

// newMainPage 构建测试主页对象（同包测试可直接访问内部状态）
func newMainPage(w fyne.Window, hub *engine.Hub) *MainPage {
	if hub == nil {
		hub = engine.NewHub()
	}
	p := &MainPage{win: w, hub: hub}

	// ===== 1. 顶部：板卡配置区 =====
	p.ipEntry = widget.NewEntry()
	p.ipEntry.SetPlaceHolder("192.168.1.100")
	p.portEntry = widget.NewEntry()
	p.portEntry.SetPlaceHolder("22")
	p.userEntry = widget.NewEntry()
	p.userEntry.SetPlaceHolder("root")
	p.passEntry = widget.NewPasswordEntry()

	ipFixed := fixSize(p.ipEntry, 150, 36)
	portFixed := fixSize(p.portEntry, 70, 36)
	userFixed := fixSize(p.userEntry, 100, 36)
	passFixed := fixSize(p.passEntry, 120, 36)

	// 打开脚本：不提供粘贴编辑；板卡差异由脚本本身区分。按钮在右侧，连接按钮在密码框后。
	p.scriptPathEntry = widget.NewEntry()
	p.scriptPathEntry.SetPlaceHolder("未选择脚本")
	p.scriptPathEntry.OnChanged = p.keepScriptPathReadOnly
	p.btnOpenScript = newGreenButton("打开脚本", p.openScript)

	// 未连接显示「连接」（Dial+hub.Set），成功后改「断开」（hub.Close）。不设独立断开按钮。
	// 连接/断开与「开始测试」同最小宽度，避免两字文案把按钮挤窄。
	p.btnConnTest = newGreenButton(connBtnConnect, p.toggleConnection)
	p.btnStart = newGreenButton(actionButtonLabel, p.startTest)
	connBtn := wrapActionWidth(p.btnConnTest)
	startBtn := wrapActionWidth(p.btnStart)

	// 连接按钮紧跟密码框；打开脚本到右侧按钮组（清空日志在原始日志标题行右上角）。
	leftCfg := container.NewHBox(
		widget.NewLabel("IP:"), ipFixed,
		widget.NewLabel("端口:"), portFixed,
		widget.NewLabel("用户名:"), userFixed,
		widget.NewLabel("密码:"), passFixed,
		connBtn,
	)
	rightCfg := container.NewHBox(
		p.btnOpenScript, startBtn,
	)
	sshRow := container.NewBorder(nil, nil, leftCfg, rightCfg, p.scriptPathEntry)
	configArea := container.NewVBox(sshRow, widget.NewSeparator())

	// ===== 2. 左侧：原始日志区 =====
	p.logEntry = widget.NewMultiLineEntry()
	p.logEntry.Wrapping = fyne.TextWrapOff      // 不换行，保持每行一条日志
	p.logEntry.Scroll = fyne.ScrollVerticalOnly // Entry 自带纵向滚动
	p.logEntry.TextStyle = fyne.TextStyle{Monospace: true}
	p.logEntry.OnChanged = p.keepLogReadOnly // 只读：用户输入被回滚
	p.btnClearLog = newGreenButton("清空日志", p.clearLog)
	logTitle := widget.NewLabelWithStyle("原始日志(stderr):", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	logBar := container.NewBorder(nil, nil, logTitle, p.btnClearLog, nil) // 左标题、右清空
	var logArea fyne.CanvasObject = container.NewBorder(logBar, nil, nil, nil, p.logEntry)
	logArea = bordered(logArea) // 灰色矩形框明确分区

	// ===== 3. 右侧：结构化测试结果表格 =====
	p.resultTable = p.newResultTable()
	p.failCountText = canvas.NewText("0", passTextColor)
	p.failCountText.TextStyle = fyne.TextStyle{Bold: true}
	tableBar := container.NewHBox(
		widget.NewLabelWithStyle("测试结果:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		p.failCountText,
	)

	// 表格左侧留6px边距，避免首列边线与分割栏重叠
	tablePad := canvas.NewRectangle(color.Transparent)
	tablePad.SetMinSize(fyne.NewSize(6, 0))
	// 首列左侧补分割竖线（Table仅在列间绘制分割线，左缘无线）。
	// 用与内部线同款 widget.Separator：颜色随主题刷新（构造期主题variant未就绪，
	// 直接取 theme.Color 会拿到深色主题的黑色分割线且不再更新）。
	// 注意不能用 Border/HBox 放置：二者会在对象间插入 theme.Padding(4dp)，
	// 使竖线与表格左缘脱开约5px，故用 edgeLayout 无间距紧贴。
	edgeLine := widget.NewSeparator()
	tableWithEdge := container.New(edgeLayout{}, edgeLine, p.resultTable)
	var tableArea fyne.CanvasObject = container.NewBorder(tableBar, nil, tablePad, nil, tableWithEdge)
	tableArea = bordered(tableArea) // 灰色矩形框明确分区

	// ===== 中部：原始日志(左) / 测试结果(右) 各占一半 =====
	mid := container.NewHSplit(logArea, tableArea)
	mid.SetOffset(0.5)

	p.root = container.NewBorder(configArea, nil, nil, nil, mid)
	p.bindHubConnButton()
	return p
}

const (
	connBtnConnect    = "连接"
	connBtnDisconnect = "断开"
)

// bindHubConnButton 订阅 Hub：连接成功改「断开」，Close/远端掉线改回「连接」。
// 回调可能在任意 goroutine，必须经 uiDo 回主线程再改文案。
func (p *MainPage) bindHubConnButton() {
	if p.hub == nil {
		return
	}
	p.hub.Subscribe(func(ok bool) {
		p.uiDo(func() { p.syncConnButton(ok) })
	})
}

// syncConnButton 按连接状态刷新按钮文案（须在主线程调用）。
func (p *MainPage) syncConnButton(connected bool) {
	if p.btnConnTest == nil {
		return
	}
	want := connBtnConnect
	if connected {
		want = connBtnDisconnect
	}
	if p.btnConnTest.Text == want {
		return
	}
	p.btnConnTest.SetText(want)
}

// ===== 业务接线 =====

// sshConfig 从输入框收集SSH配置
func (p *MainPage) sshConfig() engine.SSHConfig {
	return engine.SSHConfig{
		Host: p.ipEntry.Text,
		Port: p.portEntry.Text,
		User: p.userEntry.Text,
		Pass: p.passEntry.Text,
	}
}

// openScript 打开本地 .sh 测试脚本文件（不落地编辑，仅记录路径供测试引擎执行）。
//
// Fyne v2.8.1 的 FileDialog.Resize 会无条件调用 MinSize()→dialog.win；
// 在 Show() 之前 dialog 仍是 nil，主循环（无 recover）会因空指针直接退出。
// 必须先 Show 再 Resize。回调与 Show 全程兜底，取消/非 .sh/读失败只提示。
func (p *MainPage) openScript() {
	defer func() {
		if rec := recover(); rec != nil {
			p.appendLog(fmt.Sprintf("打开脚本异常: %v", rec))
			if p.win != nil {
				dialog.ShowError(fmt.Errorf("打开脚本失败: %v", rec), p.win)
			}
		}
	}()
	if p.win == nil {
		return
	}
	fd := dialog.NewFileOpen(p.applyOpenedScript, p.win)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".sh"}))
	if start := defaultScriptStartDir(); start != nil {
		fd.SetLocation(start)
	}
	fd.Show()
	fd.Resize(fyne.NewSize(700, 500))
}

// applyOpenedScript 处理文件选择结果：取消忽略；非 .sh 或无法读取则提示，不退出进程。
func (p *MainPage) applyOpenedScript(r fyne.URIReadCloser, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			p.appendLog(fmt.Sprintf("打开脚本异常: %v", rec))
			if p.win != nil {
				dialog.ShowError(fmt.Errorf("打开脚本失败: %v", rec), p.win)
			}
		}
	}()
	if err != nil {
		if p.win != nil {
			dialog.ShowError(err, p.win)
		}
		return
	}
	if r == nil { // 用户取消选择
		return
	}
	defer func() { _ = r.Close() }()

	uri := r.URI()
	if uri == nil {
		if p.win != nil {
			dialog.ShowError(fmt.Errorf("无效的文件路径"), p.win)
		}
		return
	}
	path := uri.Path()
	if path == "" {
		if p.win != nil {
			dialog.ShowError(fmt.Errorf("无效的文件路径"), p.win)
		}
		return
	}
	if !strings.EqualFold(filepath.Ext(path), ".sh") {
		if p.win != nil {
			dialog.ShowError(fmt.Errorf("请选择 .sh 测试脚本"), p.win)
		}
		return
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		if p.win != nil {
			dialog.ShowError(fmt.Errorf("读取脚本失败: %w", statErr), p.win)
		}
		return
	}
	if info.IsDir() {
		if p.win != nil {
			dialog.ShowError(fmt.Errorf("请选择 .sh 测试脚本"), p.win)
		}
		return
	}

	p.scriptPath = path
	if p.scriptPathEntry != nil {
		p.scriptPathEntry.SetText(path)
	}
}

// keepScriptPathReadOnly 路径框只显示完整路径，禁止改成脚本内容编辑。
func (p *MainPage) keepScriptPathReadOnly(s string) {
	want := p.scriptPath
	if s == want {
		return
	}
	if p.scriptPathEntry != nil {
		p.scriptPathEntry.SetText(want)
	}
}

// defaultScriptStartDir 优先打开项目 scripts 目录，避免从杂乱主目录起列。
func defaultScriptStartDir() fyne.ListableURI {
	var candidates []string
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "scripts"), wd)
	}
	for _, dir := range candidates {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		list, err := storage.ListerForURI(storage.NewFileURI(dir))
		if err == nil {
			return list
		}
	}
	return nil
}

// toggleConnection 未连接则 Dial 并注入 Hub；已连接则关闭共享连接。
// 按钮回调跑在 GLFW 主循环（无 recover），必须自带兜底，失败只写原始日志。
func (p *MainPage) toggleConnection() {
	defer func() {
		if r := recover(); r != nil {
			p.setRunning(false, "")
			p.appendLog(fmt.Sprintf("连接操作异常: %v", r))
		}
	}()
	if p.isRunning() {
		return
	}
	if p.hub != nil && p.hub.Connected() {
		p.disconnect()
		return
	}
	p.testConnection()
}

// testConnection 建立并保持共享 SSH 连接，结果只写原始日志（不弹窗）。
func (p *MainPage) testConnection() {
	defer func() {
		if r := recover(); r != nil {
			p.setRunning(false, "")
			p.appendLog(fmt.Sprintf("连接测试异常: %v", r))
		}
	}()
	if p.isRunning() {
		return
	}
	if p.hub != nil && p.hub.Connected() {
		p.appendLog("已有共享连接，无需重复连接")
		return
	}
	cfg := p.sshConfig()
	if _, err := cfg.Validate(); err != nil {
		p.appendLog("连接测试失败: " + err.Error())
		return
	}
	p.setRunning(true, "正在测试SSH连接...")
	go func() {
		// 兜底 recover：goroutine 内未捕获的 panic 会令整个进程退出。
		defer func() {
			if r := recover(); r != nil {
				p.uiDo(func() {
					p.setRunning(false, "")
					p.appendLog(fmt.Sprintf("连接测试异常: %v", r))
				})
			}
		}()
		client, err := engine.Dial(cfg, engine.DefaultDialTimeout)
		if err != nil {
			p.uiDo(func() {
				p.setRunning(false, "")
				p.appendLog("连接测试失败: " + err.Error())
			})
			return
		}
		// Hub.Set 在 Dial 协程调用：notify 会另起 goroutine，订阅者再 fyne.Do。
		// 切勿放进 uiDo（已在主线程）里再同步通知——订阅者 fyne.Do 会重入主循环。
		if p.hub != nil {
			p.hub.Set(client)
		}
		p.uiDo(func() {
			p.setRunning(false, "")
			p.appendLog("连接测试成功: " + cfg.Host)
		})
	}()
}

// disconnect 断开主页建立的共享 SSH 连接（终端/传输页随之失效）
func (p *MainPage) disconnect() {
	defer func() {
		if r := recover(); r != nil {
			p.appendLog(fmt.Sprintf("断开异常: %v", r))
		}
	}()
	if p.isRunning() {
		return
	}
	if p.hub == nil || !p.hub.Connected() {
		p.syncConnButton(false)
		p.appendLog("当前未连接")
		return
	}
	p.hub.Close()
	p.syncConnButton(false) // Close 的订阅通知可能异步入队，主线程先改回「连接」
	p.appendLog("已断开SSH连接")
}

// startTest 开始测试：读取脚本 → 建立SSH连接 → 双模式执行 → stderr实时上屏 → 解析结果
func (p *MainPage) startTest() {
	// 按钮回调跑在 GLFW 主循环：此处 panic 不会被后台 goroutine 的 recover 接住。
	defer func() {
		if r := recover(); r != nil {
			p.finishRun(nil, fmt.Errorf("测试启动异常: %v", r))
		}
	}()
	if p.isRunning() {
		return
	}
	cfg := p.sshConfig()
	if _, err := cfg.Validate(); err != nil {
		p.appendLog("测试失败: " + err.Error())
		return
	}
	if p.scriptPath == "" {
		p.appendLog("测试失败: 请先打开测试脚本(.sh)")
		return
	}
	script, err := os.ReadFile(p.scriptPath)
	if err != nil {
		p.appendLog("测试失败: 读取脚本失败: " + err.Error())
		return
	}

	p.clearResultTable() // 校验已过、即将 Dial/执行：先清上一轮表格，结束时由 finishRun 回填
	p.setRunning(true, "开始测试: "+filepath.Base(p.scriptPath))
	go func() {
		// 兜底 recover：goroutine 内未捕获的 panic 会令整个进程退出，
		// 此处捕获后经 uiDo 复位运行状态并写日志，确保测试流程异常不致程序崩溃。
		defer func() {
			if r := recover(); r != nil {
				p.uiDo(func() { p.finishRun(nil, fmt.Errorf("测试执行异常: %v", r)) })
			}
		}()
		client, ephemeral, err := p.dialOrReuse(cfg)
		if err != nil {
			p.uiDo(func() { p.finishRun(nil, err) })
			return
		}
		if ephemeral {
			defer client.Close()
		}
		runner := engine.NewRunner(client)
		items, runErr := runner.Run(script, func(line string) {
			p.uiDo(func() { p.appendLog(line) })
		})
		p.uiDo(func() { p.finishRun(items, runErr) })
	}()
}

// dialOrReuse 优先复用主页连接测试留下的共享连接；未连接时临时 Dial（测完关闭）。
func (p *MainPage) dialOrReuse(cfg engine.SSHConfig) (*engine.Client, bool, error) {
	if p.hub != nil {
		if c := p.hub.Client(); c != nil {
			return c, false, nil
		}
	}
	c, err := engine.Dial(cfg, engine.DefaultDialTimeout)
	return c, true, err
}

// finishRun 测试结束（主线程调用）：更新表格、恢复控件、汇总写入原始日志（不弹窗）。
// 必须自带 recover：真实 GLFW 驱动下 fyne.Do 回调在主循环执行，主循环无 recover，
// 表格刷新一旦 panic 会直接让进程退出（测试驱动里 fyne.Do 同步跑在业务协程，会被协程 recover 挡住，掩盖该问题）。
func (p *MainPage) finishRun(items []engine.TestItem, err error) {
	defer func() {
		if r := recover(); r != nil {
			p.setRunning(false, "")
			p.appendLog(fmt.Sprintf("测试收尾异常: %v", r))
		}
	}()
	p.setRunning(false, "")
	if err != nil {
		p.appendLog("测试失败: " + err.Error())
		return
	}
	if items == nil {
		items = []engine.TestItem{}
	}
	p.items = items
	if p.resultTable != nil {
		p.resultTable.Refresh()
	}
	p.setFailCount(countFailResults(items))
	pass, fail, other := 0, 0, 0
	for _, it := range items {
		switch it.Result {
		case engine.ResultPass:
			pass++
		case engine.ResultFail:
			fail++
		default:
			other++
		}
	}
	line := "测试完成: 共" + strconv.Itoa(len(items)) + "项, 通过" + strconv.Itoa(pass) +
		", 失败" + strconv.Itoa(fail)
	if other > 0 {
		line += ", 异常结果" + strconv.Itoa(other)
	}
	p.appendLog(line)
}

// uiDo 把回调派发到 Fyne 主线程，并在回调内 recover，避免主循环被 UI 更新 panic 打死。
func (p *MainPage) uiDo(fn func()) {
	fyne.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				p.setRunning(false, "")
				p.appendLog(fmt.Sprintf("界面更新异常: %v", r))
			}
		}()
		fn()
	})
}

// appendLog 追加一行原始日志并自动滚到底部（须在主线程调用，异步场景用 uiDo 包装）。
// 多行只读 Entry：SetText 触发 OnChanged 由 logUpdating 挡回；光标移到末行使内容滚底。
func (p *MainPage) appendLog(line string) {
	defer func() { _ = recover() }()
	if p.logEntry == nil {
		return
	}
	p.logBuf += line + "\n"
	p.logUpdating = true
	p.logEntry.SetText(p.logBuf)
	p.logEntry.CursorRow = strings.Count(p.logBuf, "\n") // 末行（末行下标 = 换行数）
	p.logEntry.Refresh()
	p.logUpdating = false
}

// keepLogReadOnly 维持日志区只读：用户输入/删改被回滚为缓冲内容。
// guard 防止 SetText 再次触发 OnChanged 递归。
func (p *MainPage) keepLogReadOnly(s string) {
	if p.logUpdating || s == p.logBuf {
		return
	}
	p.logUpdating = true
	if p.logEntry != nil {
		p.logEntry.SetText(p.logBuf)
	}
	p.logUpdating = false
}

// isRunning 读取运行状态
func (p *MainPage) isRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// clearResultTable 新一轮即将执行时清空结果表与不合格计数（须在主线程调用）。
func (p *MainPage) clearResultTable() {
	p.items = []engine.TestItem{}
	if p.resultTable != nil {
		p.resultTable.Refresh()
	}
	p.setFailCount(0)
}

// clearLog 清空原始日志并回到顶部（「清空日志」按钮，须在主线程调用）。
func (p *MainPage) clearLog() {
	p.logBuf = ""
	if p.logEntry == nil {
		return
	}
	p.logUpdating = true
	p.logEntry.SetText("")
	p.logEntry.CursorRow = 0
	p.logEntry.Refresh()
	p.logUpdating = false
}

// setFailCount 刷新标题旁不合格项数：0 绿字，>0 红字（须在主线程调用）。
func (p *MainPage) setFailCount(n int) {
	if p.failCountText == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	p.failCountText.Text = strconv.Itoa(n)
	if n == 0 {
		p.failCountText.Color = passTextColor
	} else {
		p.failCountText.Color = failTextColor
	}
	p.failCountText.Refresh()
}

// setRunning 更新运行状态；startLog非空时写日志（主线程调用）。
// 不用 Disable 表达忙碌：Fyne Disable 会把按钮变灰。互斥靠 running，回调里直接 return。
func (p *MainPage) setRunning(running bool, startLog string) {
	p.mu.Lock()
	p.running = running
	p.mu.Unlock()
	if running && startLog != "" {
		p.appendLog("===== " + startLog + " " + time.Now().Format("2006-01-02 15:04:05") + " =====")
	}
}

// newResultTable 构建结果表格：白底黑字保证对比度，首列为序号（从1开始），通过/未通过仅"结果"列绿/红字
func (p *MainPage) newResultTable() *widget.Table {
	t := widget.NewTable(
		func() (int, int) { return len(p.items), len(resultHeaders) },
		func() fyne.CanvasObject {
			bg := canvas.NewRectangle(rowBg)
			txt := canvas.NewText("", textColor)
			return container.NewStack(bg, container.NewPadded(txt))
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			// 表格单元格在 GLFW 绘制帧更新，主循环无 recover；类型断言失败会闪退。
			defer func() { _ = recover() }()
			if id.Row < 0 || id.Row >= len(p.items) {
				return
			}
			item := p.items[id.Row]
			bg, txt, ok := tableCellParts(o)
			if !ok {
				return
			}

			switch id.Col {
			case 0: // 序号列，从1开始
				txt.Text = strconv.Itoa(id.Row + 1)
			case 1:
				txt.Text = item.TestName
			case 2:
				txt.Text = item.TestItem
			case 3:
				txt.Text = item.Result
			case 4:
				txt.Text = item.Msg
			}
			// 仅结果列着色：通过绿字、未通过红字；其他列保持黑字
			if id.Col == 3 && isPassResult(item.Result) {
				txt.Color = passTextColor
			} else if id.Col == 3 {
				txt.Color = failTextColor
			} else {
				txt.Color = textColor
			}
			bg.FillColor = rowBg
			bg.Refresh()
			txt.Refresh()
		},
	)

	t.SetColumnWidth(0, 50) // 序号
	t.SetColumnWidth(1, 220)
	t.SetColumnWidth(2, 100)
	t.SetColumnWidth(3, 70)
	t.SetColumnWidth(4, 280)

	// 表头：浅灰底 + 黑色粗体
	t.ShowHeaderRow = true
	t.CreateHeader = func() fyne.CanvasObject {
		bg := canvas.NewRectangle(headerBg)
		l := widget.NewLabel("")
		l.TextStyle = fyne.TextStyle{Bold: true}
		return container.NewStack(bg, container.NewPadded(l))
	}
	t.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		defer func() { _ = recover() }()
		l, ok := tableHeaderLabel(o)
		if !ok {
			return
		}
		if id.Row == -1 && id.Col >= 0 && id.Col < len(resultHeaders) {
			l.Text = resultHeaders[id.Col]
		}
		l.Refresh()
	}
	return t
}

// tableCellParts 解析单元格模板：Stack(矩形底, Padded(文本))。
// 强制类型断言失败会在绘制帧打死进程，这里用逗号 ok。
func tableCellParts(o fyne.CanvasObject) (*canvas.Rectangle, *canvas.Text, bool) {
	st, ok := o.(*fyne.Container)
	if !ok || len(st.Objects) < 2 {
		return nil, nil, false
	}
	bg, ok := st.Objects[0].(*canvas.Rectangle)
	if !ok {
		return nil, nil, false
	}
	pad, ok := st.Objects[1].(*fyne.Container)
	if !ok || len(pad.Objects) < 1 {
		return nil, nil, false
	}
	txt, ok := pad.Objects[0].(*canvas.Text)
	if !ok {
		return nil, nil, false
	}
	return bg, txt, true
}

// tableHeaderLabel 解析表头模板：Stack(矩形底, Padded(Label))
func tableHeaderLabel(o fyne.CanvasObject) (*widget.Label, bool) {
	st, ok := o.(*fyne.Container)
	if !ok || len(st.Objects) < 2 {
		return nil, false
	}
	pad, ok := st.Objects[1].(*fyne.Container)
	if !ok || len(pad.Objects) < 1 {
		return nil, false
	}
	l, ok := pad.Objects[0].(*widget.Label)
	return l, ok
}

// bordered 给功能区域加灰色矩形边框，明确分区
func bordered(o fyne.CanvasObject) fyne.CanvasObject {
	frame := canvas.NewRectangle(color.Transparent)
	frame.StrokeColor = frameColor
	frame.StrokeWidth = 2
	return container.NewStack(frame, o)
}

// fixSize 固定控件尺寸（entry等在HBox中会被拉伸），返回固定尺寸的包装容器
func fixSize(o fyne.CanvasObject, w, h float32) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(w, h), o)
}

// edgeLayout 两对象布局：首对象（左缘竖线）贴左缘按自身最小宽度放置，
// 次对象（表格）紧贴其后占满剩余空间，对象间无内边距。
type edgeLayout struct{}

func (edgeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 2 {
		return
	}
	edgeW := objects[0].MinSize().Width
	objects[0].Resize(fyne.NewSize(edgeW, size.Height))
	objects[0].Move(fyne.NewPos(0, 0))
	restW := size.Width - edgeW
	if restW < 0 {
		restW = 0
	}
	objects[1].Resize(fyne.NewSize(restW, size.Height))
	objects[1].Move(fyne.NewPos(edgeW, 0))
}

func (edgeLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) != 2 {
		return fyne.NewSize(0, 0)
	}
	return fyne.NewSize(
		objects[0].MinSize().Width+objects[1].MinSize().Width,
		fyne.Max(objects[0].MinSize().Height, objects[1].MinSize().Height))
}
