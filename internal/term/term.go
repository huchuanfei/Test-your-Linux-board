package terminal

import (
	"image/color"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
	"unicode"

	widget2 "boardtest/internal/term/internal/widget"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/driver/mobile"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	bufLen             = 32768 // 32KB buffer for output, to align with modern L1 cache
	highlightBitMask   = 0x55
	maxRefreshInterval = 17 * time.Millisecond

	scrollBarWidth     = 12   // 右侧滚动条区域宽度（像素），始终预留保证列数稳定
	maxScrollbackLines = 5000 // 回滚缓冲最大行数
	wheelScrollLines   = 3    // 每格滚轮滚动行数
)

// Config is the state of a terminal, updated upon certain actions or commands.
// Use Terminal.OnConfigure hook to register for changes.
type Config struct {
	Title         string
	Rows, Columns uint
	PWD           string
}

type charSet int

const (
	charSetANSII charSet = iota
	charSetDECSpecialGraphics
	charSetAlternate
)

// Terminal is a terminal widget that loads a shell and handles input/output.
type Terminal struct {
	widget.BaseWidget
	fyne.ShortcutHandler
	content      *widget2.TermGrid
	config       Config
	listenerLock sync.Mutex
	listeners    []chan Config
	startDir     string

	pty io.Closer
	in  io.WriteCloser
	out io.Reader

	bell, debug, focused                   bool
	bold, italic, underline, strikethrough bool
	currentFG, currentBG                   color.Color
	cursorRow, cursorCol                   int
	savedRow, savedCol                     int
	scrollTop, scrollBottom                int

	cursor                   *canvas.Rectangle
	cursorHidden, bufferMode bool // buffer mode is an xterm extension that impacts control keys
	cursorMoved              func()

	// Alternate screen buffer (used by curses/fullscreen apps via ?1049h/?47h)
	altSavedGrid    []widget.TextGridRow // saved main screen rows
	altSavedRow     int                  // saved cursor row
	altSavedCol     int                  // saved cursor col
	altBufferActive bool                 // true when alternate buffer is in use

	onMouseDown, onMouseUp func(int, fyne.KeyModifier, fyne.Position)
	g0Charset              charSet
	g1Charset              charSet
	useG1CharSet           bool

	selStart, selEnd *position
	selectClipSource *selectClipboard
	blockMode        bool
	selecting        bool
	mouseCursor      desktop.Cursor

	// 回滚缓冲：滚出屏幕顶部的历史行（供滚轮回看）
	scrollback []widget.TextGridRow
	// 回滚偏移：0=实时屏幕，>0=向上回看的行数
	scrollOffset int
	// 历史回看视图网格：scrollOffset>0 时替代 content 显示
	view *widget2.TermGrid
	// 滚动条拖拽中（抑制文本选区）
	scrollbarDrag bool

	keyboardState struct {
		shiftPressed bool
		ctrlPressed  bool
		altPressed   bool
	}
	newLineMode            bool // new line mode or line feed mode
	bracketedPasteMode     bool
	disableAutoWrap        bool // disable auto wrap mode (DECAWM off)
	lastChar               rune // last graphic character output (for CSI b REP)
	state                  *parseState
	lastRefresh            time.Time
	blinking               bool
	printData              []byte
	printer                Printer
	cmd                    *exec.Cmd
	readWriterConfigurator ReadWriterConfigurator
}

// Printer is used for spooling print data when its received.
type Printer interface {
	Print([]byte)
}

// PrinterFunc is a helper function to enable easy implementation of printers.
type PrinterFunc func([]byte)

// Print calls the PrinterFunc.
func (p PrinterFunc) Print(d []byte) {
	p(d)
}

// Cursor is used for displaying a specific cursor.
func (t *Terminal) Cursor() desktop.Cursor {
	if t == nil || t.mouseCursor == nil {
		return desktop.DefaultCursor
	}
	return t.mouseCursor
}

// AcceptsTab indicates that this widget will use the Tab key (avoids loss of focus).
func (t *Terminal) AcceptsTab() bool {
	return true
}

// AddListener registers a new outgoing channel that will have our Config sent each time it changes.
func (t *Terminal) AddListener(listener chan Config) {
	t.listenerLock.Lock()
	defer t.listenerLock.Unlock()

	t.listeners = append(t.listeners, listener)
}

// MinSize provides a size large enough that a terminal could technically funcion.
func (t *Terminal) MinSize() fyne.Size {
	s := t.guessCellSize()
	return fyne.NewSize(s.Width*2.5, s.Height*1.2) // just enough to get a terminal init
}

// MouseDown handles the down action for desktop mouse events.
func (t *Terminal) MouseDown(ev *desktop.MouseEvent) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		c.Focus(t)
	}

	// 右侧滚动条：按下即定位滑块并进入拖拽模式
	if ev.Button == desktop.MouseButtonPrimary && t.inScrollbarArea(ev.Position.X) {
		t.scrollbarDrag = true
		t.scrollTo(t.scrollbarOffsetForY(ev.Position.Y))
		return
	}

	if t.hasSelectedText() {
		t.clearSelectedText()
	}
	if ev.Button == desktop.MouseButtonSecondary {
		t.pasteText(t.selectClipboard())
	}

	if t.onMouseDown == nil {
		return
	}

	if ev.Button == desktop.MouseButtonPrimary {
		t.onMouseDown(1, ev.Modifier, ev.Position)
	} else if ev.Button == desktop.MouseButtonSecondary {
		t.onMouseDown(2, ev.Modifier, ev.Position)
	}
}

// MouseUp handles the up action for desktop mouse events.
func (t *Terminal) MouseUp(ev *desktop.MouseEvent) {
	t.scrollbarDrag = false

	if t.onMouseDown == nil {
		return
	}

	if ev.Button == desktop.MouseButtonPrimary {
		t.onMouseUp(1, ev.Modifier, ev.Position)
	} else if ev.Button == desktop.MouseButtonSecondary {
		t.onMouseUp(2, ev.Modifier, ev.Position)
	}
}

// DoubleTapped handles the double tapped event.
func (t *Terminal) DoubleTapped(pe *fyne.PointEvent) {
	pos := t.sanitizePosition(pe.Position)
	termPos := t.getTermPosition(*pos)
	row, col := termPos.Row, termPos.Col

	if t.hasSelectedText() {
		t.clearSelectedText()
	}

	if row < 1 || row > len(t.activeGrid().Rows) {
		return
	}

	rowContent := t.activeGrid().Rows[row-1].Cells

	if col < 0 || col >= len(rowContent) {
		return // No valid character under the cursor, do nothing
	}

	start, end := col-1, col-1

	if !unicode.IsLetter(rowContent[start].Rune) && !unicode.IsDigit(rowContent[start].Rune) {
		return
	}

	for start > 0 && (unicode.IsLetter(rowContent[start-1].Rune) || unicode.IsDigit(rowContent[start-1].Rune)) {
		start--
	}
	if start < len(rowContent) && !unicode.IsLetter(rowContent[start].Rune) && !unicode.IsDigit(rowContent[start].Rune) {
		start++
	}
	for end < len(rowContent) && (unicode.IsLetter(rowContent[end].Rune) || unicode.IsDigit(rowContent[end].Rune)) {
		end++
	}
	if start == end {
		return
	}

	t.selStart = &position{Row: row, Col: start + 1}
	t.selEnd = &position{Row: row, Col: end}

	t.highlightSelectedText()
	t.selectClipboard().SetContent(t.SelectedText())
}

// RemoveListener de-registers a Config channel and closes it
func (t *Terminal) RemoveListener(listener chan Config) {
	t.listenerLock.Lock()
	defer t.listenerLock.Unlock()

	for i, l := range t.listeners {
		if l == listener {
			if i < len(t.listeners)-1 {
				t.listeners = append(t.listeners[:i], t.listeners[i+1:]...)
			} else {
				t.listeners = t.listeners[:i]
			}
			close(l)
			return
		}
	}
}

// Resize is called when this terminal widget has been resized.
// It ensures that the virtual terminal is within the bounds of the widget.
func (t *Terminal) Resize(s fyne.Size) {
	cellSize := t.guessCellSize()
	// 网格实际可用宽度需扣除右侧滚动条区域，避免末列被滚动条遮挡
	gridWidth := s.Width - scrollBarWidth
	if gridWidth < cellSize.Width {
		gridWidth = cellSize.Width
	}
	var cols, rows uint
	if cellSize.Width > 0 {
		cols = uint(math.Floor(float64(gridWidth) / float64(cellSize.Width)))
	}
	if cellSize.Height > 0 {
		rows = uint(math.Floor(float64(s.Height) / float64(cellSize.Height)))
	}
	// 后台 Tab / 尚未布局时 Size 可能是 0：保留上次有效行列，避免 rows=0 写屏越界。
	if cols == 0 {
		if t.config.Columns > 0 {
			cols = t.config.Columns
		} else {
			cols = defaultTermCols
		}
	}
	if rows == 0 {
		if t.config.Rows > 0 {
			rows = t.config.Rows
		} else {
			rows = defaultTermRows
		}
	}
	if (t.config.Columns == cols) && (t.config.Rows == rows) {
		t.BaseWidget.Resize(s)
		return
	}

	t.BaseWidget.Resize(s)
	gridSize := fyne.NewSize(float32(cols)*cellSize.Width, float32(rows)*cellSize.Height)
	if t.content != nil {
		t.content.Resize(gridSize)
	}
	if t.view != nil {
		t.view.Resize(gridSize)
	}

	oldRows := int(t.config.Rows)
	t.config.Columns, t.config.Rows = cols, rows
	if t.scrollBottom == 0 || t.scrollBottom == oldRows-1 {
		t.scrollBottom = int(t.config.Rows) - 1
	}
	t.onConfigure()

	t.updatePTYSize()
}

// SetDebug turns on output about terminal codes and other errors if the parameter is `true`.
func (t *Terminal) SetDebug(debug bool) {
	t.debug = debug
}

// SetStartDir can be called before one of the Run calls to specify the initial directory.
func (t *Terminal) SetStartDir(path string) {
	t.startDir = path
}

// Tapped makes sure we ask for focus if user taps us.
func (t *Terminal) Tapped(ev *fyne.PointEvent) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		c.Focus(t)
	}
}

// Text returns the contents of the buffer as a single string joined with `\n` (no style information).
func (t *Terminal) Text() string {
	return t.content.Text()
}

// pushScrollback 将滚出屏幕顶部的行深拷贝进回滚缓冲（避免与活动屏共享底层数组）
func (t *Terminal) pushScrollback(row widget.TextGridRow) {
	cells := make([]widget.TextGridCell, len(row.Cells))
	copy(cells, row.Cells)
	t.scrollback = append(t.scrollback, widget.TextGridRow{Cells: cells, Style: row.Style})
	if excess := len(t.scrollback) - maxScrollbackLines; excess > 0 {
		n := copy(t.scrollback, t.scrollback[excess:])
		t.scrollback = t.scrollback[:n]
		if t.scrollOffset > len(t.scrollback) {
			t.scrollOffset = len(t.scrollback)
		}
	}
}

// rebuildView 依据 scrollOffset 重算历史回看视图：
// 逻辑行 = 回滚缓冲(n行) + 实时屏(rows行)，视窗显示逻辑行 [n-offset, n-offset+rows)。
func (t *Terminal) rebuildView() {
	if t.content == nil || t.view == nil || t.config.Rows == 0 {
		return
	}
	rows := int(t.config.Rows)
	n := len(t.scrollback)
	if t.scrollOffset > n {
		t.scrollOffset = n
	}
	off := t.scrollOffset
	view := make([]widget.TextGridRow, rows)
	for i := 0; i < rows; i++ {
		j := n - off + i
		switch {
		case j < 0:
			view[i] = widget.TextGridRow{}
		case j < n:
			view[i] = t.scrollback[j]
		default:
			if sr := j - n; sr < len(t.content.Rows) {
				view[i] = t.content.Rows[sr]
			} else {
				view[i] = widget.TextGridRow{}
			}
		}
	}
	t.view.Rows = view
	t.view.Refresh()
}

// scrollTo 设置回滚偏移并刷新（含视图网格与滚动条）
func (t *Terminal) scrollTo(offset int) {
	if offset < 0 {
		offset = 0
	}
	if n := len(t.scrollback); offset > n {
		offset = n
	}
	if offset == t.scrollOffset {
		return
	}
	// 偏移变化前清理当前视图上的选区高亮（避免高亮索引错位残留）
	if t.hasSelectedText() {
		t.clearSelectedText()
	}
	t.scrollOffset = offset
	t.Refresh() // 渲染器负责切换视图网格并更新滚动条
}

// Scrolled 处理鼠标滚轮：鼠标上报模式（vim/htop等）转发给远端，否则滚动回滚缓冲。
// fyne 语义：Scrolled.DY>0 为滚轮向上，<0 为向下。
func (t *Terminal) Scrolled(ev *fyne.ScrollEvent) {
	if t.onMouseDown != nil { // 远端应用接管鼠标：滚轮编码为按钮64/65上报
		if ev.Scrolled.DY > 0 {
			_, _ = t.Write(t.encodeMouse(65, 0, ev.Position))
			_, _ = t.Write(t.encodeMouse(0, 0, ev.Position))
		} else if ev.Scrolled.DY < 0 {
			_, _ = t.Write(t.encodeMouse(66, 0, ev.Position))
			_, _ = t.Write(t.encodeMouse(0, 0, ev.Position))
		}
		return
	}
	if ev.Scrolled.DY > 0 { // 向上：回看更早历史
		t.scrollTo(t.scrollOffset + wheelScrollLines)
	} else if ev.Scrolled.DY < 0 { // 向下：回到实时屏
		t.scrollTo(t.scrollOffset - wheelScrollLines)
	}
}

// inScrollbarArea 判断坐标是否落在右侧滚动条区域
func (t *Terminal) inScrollbarArea(x float32) bool {
	return len(t.scrollback) > 0 && x >= t.Size().Width-scrollBarWidth
}

// scrollbarMetrics 计算滑块顶部位置与高度（像素）。offset=0 时滑块贴底，offset=n 时贴顶。
func (t *Terminal) scrollbarMetrics() (top, height float32, ok bool) {
	n := len(t.scrollback)
	rows := int(t.config.Rows)
	h := t.Size().Height
	if n == 0 || rows == 0 || h <= 0 {
		return 0, 0, false
	}
	height = float32(rows) / float32(n+rows) * h
	if height < 24 {
		height = 24
	}
	if height > h {
		height = h
	}
	span := h - height
	if span <= 0 {
		return 0, h, true
	}
	top = float32(n-t.scrollOffset) / float32(n) * span
	return top, height, true
}

// scrollbarOffsetForY 依据滚动条上的纵坐标反算回滚偏移（滑块中心对齐该坐标）
func (t *Terminal) scrollbarOffsetForY(y float32) int {
	n := len(t.scrollback)
	if n == 0 {
		return 0
	}
	_, thumbH, ok := t.scrollbarMetrics()
	if !ok {
		return 0
	}
	span := t.Size().Height - thumbH
	if span <= 0 {
		return 0
	}
	center := y - thumbH/2
	if center < 0 {
		center = 0
	} else if center > span {
		center = span
	}
	off := n - int(math.Round(float64(center/span*float32(n))))
	if off < 0 {
		off = 0
	}
	if off > n {
		off = n
	}
	return off
}

// ExitCode returns the exit code from the terminal's shell.
// Returns -1 if called before shell was started or before shell exited.
// Also returns -1 if shell was terminated by a signal.
func (t *Terminal) ExitCode() int {
	if t.cmd == nil {
		return -1
	}
	return t.cmd.ProcessState.ExitCode()
}

// TouchCancel handles the tap action for mobile apps that lose focus during tap.
func (t *Terminal) TouchCancel(ev *mobile.TouchEvent) {
	if t.onMouseUp != nil {
		t.onMouseUp(1, 0, ev.Position)
	}
}

// TouchDown handles the down action for mobile touch events.
func (t *Terminal) TouchDown(ev *mobile.TouchEvent) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		c.Focus(t)
	}

	if t.onMouseDown != nil {
		t.onMouseDown(1, 0, ev.Position)
	}
}

// TouchUp handles the up action for mobile touch events.
func (t *Terminal) TouchUp(ev *mobile.TouchEvent) {
	if t.onMouseUp != nil {
		t.onMouseUp(1, 0, ev.Position)
	}
}

func (t *Terminal) onConfigure() {
	t.listenerLock.Lock()
	defer t.listenerLock.Unlock()
	for _, l := range t.listeners {
		func() {
			defer func() { _ = recover() }() // 已关闭的 listener 发送会 panic
			select {
			case l <- t.config:
			default:
			}
		}()
	}
}

// ui 把终端绘制派到 Fyne 主线程。真实 GLFW 下回调跑在主循环（无 recover），
// 必须自带兜底：板卡一连上就会写 CSI/文本，任何空指针都不能打死进程。
func (t *Terminal) ui(fn func()) {
	fyne.Do(func() {
		defer func() { _ = recover() }()
		if t == nil || fn == nil {
			return
		}
		fn()
	})
}

const (
	defaultTermRows = 24
	defaultTermCols = 80
)

// applyDefaultSize 在控件尚未布局（后台 Tab、Size=0）时给 PTY 一个可用网格，避免 rows=0 写屏。
func (t *Terminal) applyDefaultSize() {
	if t == nil {
		return
	}
	if t.config.Columns == 0 {
		t.config.Columns = defaultTermCols
	}
	if t.config.Rows == 0 {
		t.config.Rows = defaultTermRows
	}
	if t.scrollBottom < t.scrollTop || t.scrollBottom < 0 {
		t.scrollBottom = int(t.config.Rows) - 1
	}
}

// EnsureReady 连接成功挂 PTY 前调用（须在主线程）：创建渲染器、补默认行列。
// 终端页可能不在前台，CreateRenderer/TextGrid 内层尚未就绪时往里写会空指针。
func (t *Terminal) EnsureReady() {
	if t == nil {
		return
	}
	defer func() { _ = recover() }()
	t.Refresh()
	if t.content != nil {
		_ = t.content.MinSize()
	}
	if t.view != nil {
		_ = t.view.MinSize()
	}
	if t.config.Columns == 0 || t.config.Rows == 0 {
		cell := t.guessCellSize()
		w, h := cell.Width*float32(defaultTermCols), cell.Height*float32(defaultTermRows)
		if w < 1 {
			w = 640
		}
		if h < 1 {
			h = 384
		}
		t.Resize(fyne.NewSize(w, h))
	}
	if t.config.Columns == 0 || t.config.Rows == 0 {
		t.applyDefaultSize()
	}
}

func (t *Terminal) open() error {
	for t.config.Columns <= 2 { // wait until it has a valid area
		time.Sleep(time.Millisecond * 10)
	}
	in, out, pty, err := t.startPTY()
	if err != nil {
		return err
	}

	t.in, t.out = in, out
	if t.readWriterConfigurator != nil {
		t.out, t.in = t.readWriterConfigurator.SetupReadWriter(out, in)
	}

	t.pty = pty

	t.updatePTYSize()
	return nil
}

// Exit requests that this terminal exits.
// If there are embedded shells it will exit the child one only.
func (t *Terminal) Exit() {
	_, _ = t.Write([]byte{0x4})
}

func (t *Terminal) close() error {
	if t.in != t.pty {
		_ = t.in.Close() // we may already be closed
	}
	if t.pty == nil {
		return nil
	}

	return t.pty.Close()
}

// don't call often - should we cache?
func (t *Terminal) guessCellSize() fyne.Size {
	cell := canvas.NewText("M", color.White)
	cell.TextStyle.Monospace = true

	scale := t.Theme().Size(theme.SizeNameText) / theme.TextSize()
	min := cell.MinSize()
	return fyne.NewSize(float32(math.Round(float64(min.Width*scale))), float32(math.Round(float64(min.Height*scale))))
}

func (t *Terminal) run() {
	buf := make([]byte, bufLen)
	var leftOver []byte
	for {
		num, err := t.out.Read(buf)
		if err != nil {
			if t.cmd != nil {
				// wait for cmd (shell) to exit, populates ProcessState.ExitCode
				t.cmd.Wait()
			}
			if err == io.EOF || err.Error() == "EOF" {
				break // term exit on macOS
			} else if err, ok := err.(*os.PathError); ok &&
				(err.Err.Error() == "input/output error" || err.Err.Error() == "file already closed") {
				break // broken pipe, terminal exit
			}

			fyne.LogError("pty read error", err)
		}

		lenLeftOver := len(leftOver)
		fullBuf := buf
		if lenLeftOver > 0 {
			fullBuf = append(leftOver, buf[:num]...)
			num += lenLeftOver
		}

		if t.content == nil {
			return
		}

		leftOver = t.handleOutput(fullBuf[:num])
		if len(leftOver) == 0 || time.Since(t.lastRefresh) > maxRefreshInterval {
			t.lastRefresh = time.Now()
			fyne.DoAndWait(func() {
				defer func() { _ = recover() }()
				t.Refresh()
			})
		}
	}
}

// RunLocalShell starts the terminal by loading a shell and starting to process the input/output.
func (t *Terminal) RunLocalShell() error {
	if t.startDir != "" {
		t.config.PWD = t.startDir
	} else {
		t.config.PWD, _ = os.Getwd()
	}
	for t.config.Columns == 0 { // don't load the TTY until our output is configured
		time.Sleep(time.Millisecond * 50)
	}
	err := t.open()
	if err != nil {
		return err
	}

	t.run()

	return t.close()
}

// RunWithConnection starts the terminal by connecting to an external resource like an SSH connection.
func (t *Terminal) RunWithConnection(in io.WriteCloser, out io.Reader) error {
	if t.startDir != "" {
		t.config.PWD = t.startDir
	} else {
		t.config.PWD, _ = os.Getwd()
	}
	deadline := time.Now().Add(2 * time.Second)
	for t.config.Columns == 0 { // don't load the TTY until our output is configured
		if time.Now().After(deadline) {
			t.applyDefaultSize()
			break
		}
		time.Sleep(time.Millisecond * 50)
	}
	t.in, t.out = in, out
	if t.readWriterConfigurator != nil {
		t.out, t.in = t.readWriterConfigurator.SetupReadWriter(out, in)
	}

	t.run()

	return t.close()
}

// Write is used to send commands into an open terminal connection.
// Errors will be returned if the connection is not established, has closed, or there was a problem in transmission.
func (t *Terminal) Write(b []byte) (int, error) {
	if t.in == nil {
		return 0, io.EOF
	}

	return t.in.Write(b)
}

func (t *Terminal) setupShortcuts() {
	var paste fyne.Shortcut
	paste = &desktop.CustomShortcut{KeyName: fyne.KeyV, Modifier: fyne.KeyModifierShift | fyne.KeyModifierShortcutDefault}
	if runtime.GOOS == "darwin" {
		paste = &fyne.ShortcutPaste{} // we look up clipboard later
	}
	t.ShortcutHandler.AddShortcut(paste,
		func(sh fyne.Shortcut) {
			clip := fyne.CurrentApp().Clipboard()
			if ps, ok := sh.(*fyne.ShortcutPaste); ok && ps.Secondary {
				clip = t.selectClipboard()
			}
			t.pasteText(clip)
		})
	var shortcutCopy fyne.Shortcut
	shortcutCopy = &desktop.CustomShortcut{KeyName: fyne.KeyC, Modifier: fyne.KeyModifierShift | fyne.KeyModifierShortcutDefault}
	if runtime.GOOS == "darwin" {
		shortcutCopy = &fyne.ShortcutCopy{} // we look up clipboard later
	}

	t.ShortcutHandler.AddShortcut(shortcutCopy,
		func(_ fyne.Shortcut) {
			t.copySelectedText(fyne.CurrentApp().Clipboard())
		})
}

func (t *Terminal) startingDir() string {
	if t.startDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}

	return t.startDir
}

// New sets up a new terminal instance with the bash shell
func New() *Terminal {
	t := &Terminal{
		mouseCursor: desktop.DefaultCursor,
		in:          discardWriter{},
	}
	t.ExtendBaseWidget(t)

	return t
}

// sanitizePosition ensures that the given position p is within the bounds of the terminal.
// If the position is outside the bounds, it adjusts the coordinates to the nearest valid values.
// The adjusted position is then returned.
func (t *Terminal) sanitizePosition(p fyne.Position) *fyne.Position {
	size := t.Size()
	width, height := size.Width, size.Height
	if p.X < 0 {
		p.X = 0
	} else if p.X > width {
		p.X = width
	}

	if p.Y < 0 {
		p.Y = 0
	} else if p.Y > height {
		p.Y = height
	}

	return &p
}

// Dragged is called by fyne when the left mouse is down and moved whilst over the widget.
func (t *Terminal) Dragged(d *fyne.DragEvent) {
	// 滚动条拖拽：滑块中心跟随鼠标
	if t.scrollbarDrag {
		pos := t.sanitizePosition(d.Position)
		t.scrollTo(t.scrollbarOffsetForY(pos.Y))
		return
	}

	pos := t.sanitizePosition(d.Position)
	if !t.selecting {
		if t.keyboardState.altPressed {
			t.blockMode = true
		}
		p := t.getTermPosition(*pos)
		t.selStart = &p
		t.selEnd = nil
	}
	// clear any previous selection
	sr, sc, er, ec := t.getSelectedRange()
	widget2.ClearHighlightRange(t.activeGrid(), t.blockMode, sr, sc, er, ec)

	// make sure that x,y,x1,y1 are always positive
	t.selecting = true
	t.mouseCursor = desktop.TextCursor
	p := t.getTermPosition(*pos)
	t.selEnd = &p
	t.highlightSelectedText()
}

// DragEnd is called by fyne when the left mouse is released after a Drag event.
func (t *Terminal) DragEnd() {
	if t.scrollbarDrag {
		t.scrollbarDrag = false
		return
	}
	if t.selecting {
		t.selectClipboard().SetContent(t.SelectedText())
	}
	t.selecting = false
}

// SetReadWriter sets the readWriterConfigurator function that will be used when creating a new terminal.
// The readWriterConfigurator function is responsible for setting up the I/O readers and writers.
func (t *Terminal) SetReadWriter(mw ReadWriterConfigurator) {
	t.readWriterConfigurator = mw
}

// ReadWriterConfigurator is an interface that defines the methods required to set up
// the input (reader) and output (writer) streams for the terminal.
// Implementations of this interface can modify or wrap the reader and writer.
type ReadWriterConfigurator interface {
	// SetupReadWriter configures the input and output streams for the terminal.
	// It takes an input reader (r) and an output writer (w) as arguments.
	// The function returns a possibly modified reader and writer that
	// the terminal will use for I/O operations.
	SetupReadWriter(r io.Reader, w io.WriteCloser) (io.Reader, io.WriteCloser)
}

// ReadWriterConfiguratorFunc is a function type that matches the signature of the
// SetupReadWriter method in the Middleware interface.
type ReadWriterConfiguratorFunc func(r io.Reader, w io.WriteCloser) (io.Reader, io.WriteCloser)

// SetupReadWriter allows ReadWriterConfiguratorFunc to satisfy the Middleware interface.
// It calls the ReadWriterConfiguratorFunc itself.
func (m ReadWriterConfiguratorFunc) SetupReadWriter(r io.Reader, w io.WriteCloser) (io.Reader, io.WriteCloser) {
	return m(r, w)
}
