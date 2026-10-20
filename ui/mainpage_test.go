package ui

import (
	"os"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"boardtest/engine"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// newTestMainPage 构建测试主页（使用Fyne测试驱动，无需真实显示）
func newTestMainPage(t *testing.T) *MainPage {
	t.Helper()
	testApp := test.NewApp()
	win := testApp.NewWindow("")
	p := newMainPage(win, nil)
	win.SetContent(p.root)
	win.Resize(fyne.NewSize(1200, 700))
	return p
}

// TestMainPageTestConnectionFail 连接失败：running 状态复位、按钮恢复、不崩溃
func TestMainPageTestConnectionFail(t *testing.T) {
	p := newTestMainPage(t)

	p.ipEntry.SetText("127.0.0.1")
	p.portEntry.SetText("1") // 保留端口，无服务监听 → 连接拒绝
	p.userEntry.SetText("u")
	p.passEntry.SetText("p")

	p.toggleConnection()

	if !waitFor(5*time.Second, func() bool { return !p.isRunning() }) {
		t.Fatalf("连接失败后 running 状态未复位")
	}
	assertButtonsStayEnabled(t, p)
	if p.btnConnTest.Text != connBtnConnect {
		t.Fatalf("连接失败后文案=%q，应保持「连接」", p.btnConnTest.Text)
	}
}

// TestMainPageStartTestDialFail 开始测试连接失败：running 复位、提示错误、不崩溃
func TestMainPageStartTestDialFail(t *testing.T) {
	p := newTestMainPage(t)
	p.ipEntry.SetText("127.0.0.1")
	p.portEntry.SetText("1")
	p.userEntry.SetText("u")
	p.passEntry.SetText("p")
	p.scriptPath = t.TempDir() + "/dummy.sh"
	if err := os.WriteFile(p.scriptPath, []byte("echo ok\n"), 0o644); err != nil {
		t.Fatalf("写脚本失败: %v", err)
	}

	p.startTest()

	if !waitFor(5*time.Second, func() bool { return !p.isRunning() }) {
		t.Fatalf("连接失败后 running 状态未复位")
	}
	assertButtonsStayEnabled(t, p)
}

// TestMainPageStartTestNoScript 未选脚本：不进入 running、不崩溃
func TestMainPageStartTestNoScript(t *testing.T) {
	p := newTestMainPage(t)
	p.ipEntry.SetText("127.0.0.1")
	p.portEntry.SetText("22")
	p.userEntry.SetText("u")
	p.passEntry.SetText("p")

	p.startTest()

	if p.isRunning() {
		t.Fatal("未选脚本不应进入 running")
	}
}

// TestAppendLogManyLines 连续写日志不得 panic（覆盖开始测试 stderr 流式上屏），且内容可读可清空
func TestAppendLogManyLines(t *testing.T) {
	p := newTestMainPage(t)
	for i := 0; i < 200; i++ {
		p.appendLog("log line")
	}
	if p.logEntry == nil {
		t.Fatal("应有可选中拷贝的日志控件")
	}
	if !strings.Contains(p.logEntry.Text, "log line") {
		t.Fatal("日志内容应写入控件")
	}
	p.clearLog()
	if got := p.logEntry.Text; got != "" {
		t.Fatalf("清空日志后应为空, got %q", got)
	}
}

// TestAppendLogReadOnly 日志区只读：模拟用户输入被回滚，缓冲内容不变
func TestAppendLogReadOnly(t *testing.T) {
	p := newTestMainPage(t)
	p.appendLog("hello")
	// 模拟用户键入：OnChanged 收到被改写的文本，应被回滚
	p.keepLogReadOnly("helloINTRUDER")
	if got := p.logEntry.Text; got != "hello\n" {
		t.Fatalf("日志区应保持只读, got %q", got)
	}
}

func TestIsPassResult(t *testing.T) {
	for _, s := range []string{"pass", "PASS", " Pass ", "通过", "success", "ok", "true"} {
		if !isPassResult(s) {
			t.Fatalf("%q 应为通过态", s)
		}
	}
	for _, s := range []string{"fail", "FAIL", "error", ""} {
		if isPassResult(s) {
			t.Fatalf("%q 不应为通过态", s)
		}
	}
}

func TestIsFailResult(t *testing.T) {
	for _, s := range []string{"fail", "FAIL", "未通过", "失败", "error", "failed", "false"} {
		if !isFailResult(s) {
			t.Fatalf("%q 应为不合格", s)
		}
	}
	for _, s := range []string{"pass", "PASS", "通过", "ok", ""} {
		if isFailResult(s) {
			t.Fatalf("%q 不应为不合格", s)
		}
	}
}

func TestFailCountBesideResultTitle(t *testing.T) {
	p := newTestMainPage(t)
	if p.failCountText == nil || p.failCountText.Text != "0" || p.failCountText.Color != passTextColor {
		t.Fatalf("初始不合格数应为绿色 0, got text=%v color=%v", p.failCountText, colorOf(p.failCountText))
	}

	p.finishRun([]engine.TestItem{
		{TestName: "A", TestItem: "a", Result: engine.ResultPass, Msg: "ok"},
		{TestName: "B", TestItem: "b", Result: engine.ResultFail, Msg: "x"},
		{TestName: "C", TestItem: "c", Result: "FAIL", Msg: "x"},
		{TestName: "D", TestItem: "d", Result: "未通过", Msg: "x"},
		{TestName: "E", TestItem: "e", Result: "失败", Msg: "x"},
		{TestName: "F", TestItem: "f", Result: "", Msg: "empty"},
		{TestName: "G", TestItem: "g", Result: "error", Msg: "x"},
	}, nil)
	if p.failCountText.Text != "5" || p.failCountText.Color != failTextColor {
		t.Fatalf("5 项不合格应为红字 5, got text=%q color=%v", p.failCountText.Text, p.failCountText.Color)
	}

	p.finishRun([]engine.TestItem{
		{TestName: "A", TestItem: "a", Result: engine.ResultPass, Msg: "ok"},
	}, nil)
	if p.failCountText.Text != "0" || p.failCountText.Color != passTextColor {
		t.Fatalf("全通过应为绿色 0, got text=%q color=%v", p.failCountText.Text, p.failCountText.Color)
	}

	p.finishRun([]engine.TestItem{
		{TestName: "B", TestItem: "b", Result: engine.ResultFail, Msg: "x"},
	}, nil)
	p.ipEntry.SetText("127.0.0.1")
	p.portEntry.SetText("1")
	p.userEntry.SetText("u")
	p.passEntry.SetText("p")
	p.scriptPath = t.TempDir() + "/dummy.sh"
	if err := os.WriteFile(p.scriptPath, []byte("echo ok\n"), 0o644); err != nil {
		t.Fatalf("写脚本失败: %v", err)
	}
	p.startTest()
	if p.failCountText.Text != "0" || p.failCountText.Color != passTextColor {
		t.Fatalf("开始新测试应清零为绿色 0, got text=%q color=%v", p.failCountText.Text, p.failCountText.Color)
	}
	if len(p.items) != 0 {
		t.Fatalf("开始新测试应清空结果表, 仍有 %d 行", len(p.items))
	}
	if !waitFor(5*time.Second, func() bool { return !p.isRunning() }) {
		t.Fatalf("连接失败后 running 状态未复位")
	}
}

// TestStartTestClearsResultTable 校验通过即将执行时清表；校验失败不清。
func TestStartTestClearsResultTable(t *testing.T) {
	p := newTestMainPage(t)
	old := []engine.TestItem{
		{TestName: "旧项", TestItem: "old", Result: engine.ResultFail, Msg: "x"},
	}
	p.items = old
	if p.resultTable != nil {
		p.resultTable.Refresh()
	}
	p.setFailCount(1)
	_ = test.RenderObjectToMarkup(p.resultTable)

	p.startTest() // 未填配置 / 未选脚本 → 校验失败
	if p.isRunning() {
		t.Fatal("校验失败不应进入 running")
	}
	if len(p.items) != 1 || p.items[0].TestName != "旧项" {
		t.Fatalf("校验失败不应清表, items=%v", p.items)
	}
	if p.failCountText.Text != "1" {
		t.Fatalf("校验失败不应清零不合格数, got %q", p.failCountText.Text)
	}

	p.ipEntry.SetText("127.0.0.1")
	p.portEntry.SetText("1")
	p.userEntry.SetText("u")
	p.passEntry.SetText("p")
	p.scriptPath = t.TempDir() + "/dummy.sh"
	if err := os.WriteFile(p.scriptPath, []byte("echo ok\n"), 0o644); err != nil {
		t.Fatalf("写脚本失败: %v", err)
	}
	p.startTest()
	if len(p.items) != 0 {
		t.Fatalf("即将执行时应清表, 仍有 %d 行", len(p.items))
	}
	if p.failCountText.Text != "0" || p.failCountText.Color != passTextColor {
		t.Fatalf("即将执行时应回到绿色 0, got text=%q color=%v", p.failCountText.Text, p.failCountText.Color)
	}
	_ = test.RenderObjectToMarkup(p.resultTable)
	if !waitFor(5*time.Second, func() bool { return !p.isRunning() }) {
		t.Fatalf("连接失败后 running 状态未复位")
	}
}

// TestClearLogButtonOnLogBarNotToolbar 「清空日志」在原始日志标题行右上角，不在顶部工具栏。
func TestClearLogButtonOnLogBarNotToolbar(t *testing.T) {
	p := newTestMainPage(t)
	if p.btnClearLog == nil || p.btnClearLog.Text != "清空日志" {
		t.Fatalf("应有清空日志按钮, got %v", p.btnClearLog)
	}
	if p.btnClearLog.Importance != widget.SuccessImportance {
		t.Fatal("清空日志应保持绿色按钮")
	}
	texts := collectButtonTexts(p.root)
	if countLabel(texts, "清空日志") != 1 {
		t.Fatalf("应有且仅有一个清空日志, 实际=%v", texts)
	}

	bar := findParentContainer(p.root, p.btnClearLog)
	if bar == nil {
		t.Fatal("找不到清空日志所在容器")
	}
	if !containerHasLabel(bar, "原始日志") {
		t.Fatal("清空日志应与「原始日志」标题同一行（右上角）")
	}
	if containerHasButton(bar, "打开脚本") || containerHasButton(bar, actionButtonLabel) {
		t.Fatal("清空日志不应仍在顶部工具栏（打开脚本/开始测试一排）")
	}
	topRight := findParentContainer(p.root, p.btnStart)
	if topRight != nil && containerHasButton(topRight, "清空日志") {
		t.Fatal("顶部开始测试一排不应再含清空日志")
	}

	p.appendLog("keep-me")
	p.btnClearLog.OnTapped()
	if got := p.logEntry.Text; got != "" {
		t.Fatalf("清空日志后应为空, got %q", got)
	}
}

func colorOf(txt *canvas.Text) any {
	if txt == nil {
		return nil
	}
	return txt.Color
}

func TestResultColumnPassGreenFailRed(t *testing.T) {
	p := newTestMainPage(t)
	p.items = []engine.TestItem{
		{TestName: "A", TestItem: "a", Result: engine.ResultPass, Msg: "ok"},
		{TestName: "B", TestItem: "b", Result: engine.ResultFail, Msg: "x"},
	}
	cell := p.resultTable.CreateCell()
	p.resultTable.UpdateCell(widget.TableCellID{Row: 0, Col: 3}, cell)
	_, txt, ok := tableCellParts(cell)
	if !ok || txt.Color != passTextColor {
		t.Fatalf("pass 结果列应为绿字, color=%v ok=%v", txt, ok)
	}
	p.resultTable.UpdateCell(widget.TableCellID{Row: 0, Col: 1}, cell)
	_, txt, ok = tableCellParts(cell)
	if !ok || txt.Color != textColor {
		t.Fatal("通过项非结果列应保持黑字")
	}
	p.resultTable.UpdateCell(widget.TableCellID{Row: 1, Col: 3}, cell)
	_, txt, ok = tableCellParts(cell)
	if !ok || txt.Color != failTextColor {
		t.Fatalf("fail 结果列应为红字, color=%v ok=%v", txt, ok)
	}
}

// TestTableCallbacksTolerateBadTemplate 单元格/表头回调遇到非预期对象不得 panic
func TestTableCallbacksTolerateBadTemplate(t *testing.T) {
	p := newTestMainPage(t)
	p.items = []engine.TestItem{{TestName: "A", TestItem: "a", Result: engine.ResultFail, Msg: "x"}}
	p.resultTable.Refresh()
	_ = test.RenderObjectToMarkup(p.resultTable)

	if _, _, ok := tableCellParts(widget.NewLabel("bad")); ok {
		t.Fatal("错误模板不应解析为单元格")
	}
	if _, ok := tableHeaderLabel(widget.NewLabel("bad")); ok {
		t.Fatal("错误模板不应解析为表头")
	}
}

// TestMainPageTestConnectionInvalidConfig 无效配置：不发起连接，直接返回
func TestMainPageTestConnectionInvalidConfig(t *testing.T) {
	p := newTestMainPage(t)

	// 空端口 → Validate 失败，不应进入 running
	p.ipEntry.SetText("127.0.0.1")
	p.portEntry.SetText("")
	p.userEntry.SetText("u")
	p.passEntry.SetText("p")

	p.toggleConnection()

	if p.isRunning() {
		t.Fatal("无效配置不应进入 running 状态")
	}
}

// collectButtonTexts 按对象树深度优先收集按钮文案（Border 先中再左再右：左组在右组前）。
func collectButtonTexts(o fyne.CanvasObject) []string {
	var out []string
	walkCanvas(o, func(b *widget.Button) {
		out = append(out, b.Text)
	})
	return out
}

// walkCanvas 遍历容器与 HSplit/VSplit，对每个按钮回调。
func walkCanvas(o fyne.CanvasObject, onBtn func(*widget.Button)) {
	if o == nil {
		return
	}
	if b, ok := o.(*widget.Button); ok {
		onBtn(b)
		return
	}
	switch c := o.(type) {
	case *fyne.Container:
		for _, ch := range c.Objects {
			walkCanvas(ch, onBtn)
		}
	case *container.Split:
		walkCanvas(c.Leading, onBtn)
		walkCanvas(c.Trailing, onBtn)
	}
}

// TestMainPageTestConnectionSuccess 成功连接：状态复位、日志记录成功、不崩溃
func TestMainPageTestConnectionSuccess(t *testing.T) {
	addr := startUITermSSHServer(t)
	p := newTestMainPage(t)

	p.ipEntry.SetText(addr.host)
	p.portEntry.SetText(addr.port)
	p.userEntry.SetText("tester")
	p.passEntry.SetText("secret")

	p.toggleConnection()

	if !waitFor(5*time.Second, func() bool { return !p.isRunning() && p.hub.Connected() }) {
		t.Fatalf("成功连接后 running 状态未复位或未保持共享连接")
	}
	assertButtonsStayEnabled(t, p)
	if !waitFor(2*time.Second, func() bool { return p.btnConnTest.Text == connBtnDisconnect }) {
		t.Fatalf("连接成功后文案=%q，期望「断开」", p.btnConnTest.Text)
	}
	t.Cleanup(func() { p.hub.Close() })
}

func TestMainPageDisconnectClearsSharedConn(t *testing.T) {
	addr := startUITermSSHServer(t)
	p := newTestMainPage(t)
	p.ipEntry.SetText(addr.host)
	p.portEntry.SetText(addr.port)
	p.userEntry.SetText("tester")
	p.passEntry.SetText("secret")
	p.toggleConnection()
	if !waitFor(5*time.Second, func() bool { return p.hub.Connected() }) {
		t.Fatal("应先连接成功")
	}
	if !waitFor(2*time.Second, func() bool { return p.btnConnTest.Text == connBtnDisconnect }) {
		t.Fatalf("连接成功后文案=%q，期望「断开」", p.btnConnTest.Text)
	}
	p.toggleConnection() // 已连接时再点 = 断开
	if p.hub.Connected() {
		t.Fatal("断开后共享连接应清空")
	}
	if p.btnConnTest.Text != connBtnConnect {
		t.Fatalf("断开后文案=%q，应回到「连接」", p.btnConnTest.Text)
	}
}

func TestConnToggleButtonLabelAndLayout(t *testing.T) {
	p := newTestMainPage(t)
	if p.btnConnTest == nil || p.btnConnTest.Text != connBtnConnect {
		t.Fatalf("默认文案=%v，期望「连接」", p.btnConnTest)
	}
	texts := collectButtonTexts(p.root)
	for _, s := range texts {
		if s == "连接/断开" || s == "连接测试" {
			t.Fatalf("不应再出现固定文案或独立按钮 %q, 实际=%v", s, texts)
		}
	}
	if countLabel(texts, connBtnDisconnect) != 0 {
		t.Fatalf("未连接时不应出现「断开」按钮, 实际=%v", texts)
	}
	connIdx, openIdx := -1, -1
	for i, s := range texts {
		if s == connBtnConnect {
			connIdx = i
		}
		if s == "打开脚本" {
			openIdx = i
		}
	}
	if connIdx < 0 || openIdx < 0 {
		t.Fatalf("缺少按钮: %v", texts)
	}
	if connIdx >= openIdx {
		t.Fatalf("「连接」应在「打开脚本」左边（密码框后），实际=%v", texts)
	}

	// 连接/断开与开始测试同最小宽度（包装容器，不随文案「断开」变窄）
	connWrap := findParentContainer(p.root, p.btnConnTest)
	startWrap := findParentContainer(p.root, p.btnStart)
	if connWrap == nil || startWrap == nil {
		t.Fatal("连接/开始测试应有同宽包装")
	}
	if connWrap.MinSize().Width != startWrap.MinSize().Width {
		t.Fatalf("连接按钮宽 %v 应与开始测试 %v 一致", connWrap.MinSize().Width, startWrap.MinSize().Width)
	}
	p.syncConnButton(true)
	if p.btnConnTest.Text != connBtnDisconnect {
		t.Fatalf("同步后文案=%q，期望「断开」", p.btnConnTest.Text)
	}
	if connWrap.MinSize().Width != startWrap.MinSize().Width {
		t.Fatalf("断开后面宽 %v 应仍与开始测试 %v 一致", connWrap.MinSize().Width, startWrap.MinSize().Width)
	}
}

func TestSetRunningDoesNotDisableButtons(t *testing.T) {
	p := newTestMainPage(t)
	p.setRunning(true, "busy")
	assertButtonsStayEnabled(t, p)
	if !p.isRunning() {
		t.Fatal("setRunning(true) 后应进入 running")
	}
	p.startTest() // running 防重入，不应改状态
	if !p.isRunning() {
		t.Fatal("忙碌中再次开始测试应被忽略")
	}
	p.toggleConnection()
	if !p.isRunning() {
		t.Fatal("忙碌中连接操作应被忽略")
	}
	p.setRunning(false, "")
	assertButtonsStayEnabled(t, p)
}

func TestConnButtonFollowsHubCloseAndRemoteDrop(t *testing.T) {
	addr := startUITermSSHServer(t)
	p := newTestMainPage(t)
	p.ipEntry.SetText(addr.host)
	p.portEntry.SetText(addr.port)
	p.userEntry.SetText("tester")
	p.passEntry.SetText("secret")
	p.toggleConnection()
	if !waitFor(5*time.Second, func() bool {
		return p.hub.Connected() && p.btnConnTest.Text == connBtnDisconnect
	}) {
		t.Fatalf("连接成功后文案=%q connected=%v", p.btnConnTest.Text, p.hub.Connected())
	}

	p.hub.Close()
	if !waitFor(2*time.Second, func() bool { return p.btnConnTest.Text == connBtnConnect }) {
		t.Fatalf("Hub.Close 后文案=%q，应回到「连接」", p.btnConnTest.Text)
	}

	p.toggleConnection()
	if !waitFor(5*time.Second, func() bool {
		return p.hub.Connected() && p.btnConnTest.Text == connBtnDisconnect
	}) {
		t.Fatal("再次连接应成功并显示「断开」")
	}
	c := p.hub.Client()
	if c == nil {
		t.Fatal("应有共享连接")
	}
	_ = c.Close()
	if !waitFor(3*time.Second, func() bool {
		return !p.hub.Connected() && p.btnConnTest.Text == connBtnConnect
	}) {
		t.Fatalf("远端掉线后 connected=%v 文案=%q，应回到「连接」", p.hub.Connected(), p.btnConnTest.Text)
	}
}

// TestConnectSuccessWithHubAndTerminal 复现真实 app：主页 + 终端 + 传输共用 Hub，
// 连接成功走 Hub.Set 同步通知订阅者（终端页默认不在前台）。不得 panic。
func TestConnectSuccessWithHubAndTerminal(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("连接成功路径 panic: %v\n%s", r, debug.Stack())
		}
	}()

	addr := startUITermSSHServer(t)
	a := test.NewApp()
	w := a.NewWindow("")
	hub := engine.NewHub()

	main := newMainPage(w, hub)
	term := newTerminalPage(w, hub)
	xfer := newTransferPage(w, hub)
	tabs := container.NewAppTabs(
		container.NewTabItem("测试主页", main.root),
		container.NewTabItem("SSH终端", term.root),
		container.NewTabItem("文件传输", xfer.root),
	)
	w.SetContent(tabs)
	w.Resize(fyne.NewSize(1240, 760))

	main.ipEntry.SetText(addr.host)
	main.portEntry.SetText(addr.port)
	main.userEntry.SetText("tester")
	main.passEntry.SetText("secret")

	main.toggleConnection()

	if !waitFor(8*time.Second, func() bool { return !main.isRunning() && hub.Connected() }) {
		t.Fatalf("成功连接后应保持共享连接 running=%v connected=%v", main.isRunning(), hub.Connected())
	}
	assertButtonsStayEnabled(t, main)
	if !waitFor(2*time.Second, func() bool { return main.btnConnTest.Text == connBtnDisconnect }) {
		t.Fatalf("连接成功后文案=%q，期望「断开」", main.btnConnTest.Text)
	}
	t.Cleanup(func() { hub.Close() })

	// 终端页默认不在前台：EnsureReady 不得因未布局/未建渲染器 panic，且不得回滚 SSH。
	if term.term != nil {
		mustNoPanicUI(t, "EnsureReady", term.term.EnsureReady)
	}
	if !hub.Connected() {
		t.Fatal("终端挂接失败不得回滚已成功的 SSH")
	}
	if xfer.status == nil || xfer.status.Text == "" {
		t.Fatal("传输页应收到订阅通知")
	}
}

func assertButtonsStayEnabled(t *testing.T, p *MainPage) {
	t.Helper()
	for _, b := range []*widget.Button{p.btnOpenScript, p.btnConnTest, p.btnStart} {
		if b == nil {
			t.Fatal("按钮不应为 nil")
		}
		if b.Disabled() {
			t.Fatalf("%q 不应 Disable（Fyne Disable 会变灰）", b.Text)
		}
	}
}

func findParentContainer(root fyne.CanvasObject, target fyne.CanvasObject) *fyne.Container {
	var found *fyne.Container
	var walk func(fyne.CanvasObject)
	walk = func(obj fyne.CanvasObject) {
		if found != nil || obj == nil {
			return
		}
		switch c := obj.(type) {
		case *fyne.Container:
			for _, ch := range c.Objects {
				if ch == target {
					found = c
					return
				}
				walk(ch)
			}
		case *container.Split:
			walk(c.Leading)
			walk(c.Trailing)
		}
	}
	walk(root)
	return found
}

func containerHasLabel(c *fyne.Container, substr string) bool {
	if c == nil {
		return false
	}
	for _, o := range c.Objects {
		if l, ok := o.(*widget.Label); ok && containsStr(l.Text, substr) {
			return true
		}
	}
	return false
}

func containerHasButton(c *fyne.Container, text string) bool {
	if c == nil {
		return false
	}
	for _, o := range c.Objects {
		if b, ok := o.(*widget.Button); ok && b.Text == text {
			return true
		}
	}
	return false
}

func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}

func countLabel(texts []string, want string) int {
	n := 0
	for _, s := range texts {
		if s == want {
			n++
		}
	}
	return n
}

func mustNoPanicUI(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panic: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
}
