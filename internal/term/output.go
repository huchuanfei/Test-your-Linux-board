package terminal

import (
	"bytes"
	"log"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	widget2 "boardtest/internal/term/internal/widget"
	"github.com/mattn/go-runewidth"
)

const (
	asciiBell      = 7
	asciiBackspace = 8
	asciiEscape    = 27

	noEscape = 5000
	tabWidth = 8
)

var charSetMap = map[charSet]func(rune) rune{
	charSetANSII: func(r rune) rune {
		return r
	},
	charSetDECSpecialGraphics: func(r rune) rune {
		m, ok := decSpecialGraphics[r]
		if ok {
			return m
		}
		return r
	},
	charSetAlternate: func(r rune) rune {
		return r
	},
}

var specialChars = map[rune]func(t *Terminal){
	asciiBell:      handleOutputBell,
	asciiBackspace: handleOutputBackspace,
	'\n':           handleOutputLineFeed,
	'\v':           handleOutputLineFeed,
	'\f':           handleOutputLineFeed,
	'\r':           handleOutputCarriageReturn,
	'\t':           handleOutputTab,
	0x0e:           handleShiftOut, // handle switch to G1 character set
	0x0f:           handleShiftIn,  // handle switch to G0 character set
}

// decSpecialGraphics is for ESC(0 graphics mode
// https://en.wikipedia.org/wiki/DEC_Special_Graphics
var decSpecialGraphics = map[rune]rune{
	'`': '◆', // filled in diamond
	'a': '▒', // filled in box
	'b': '␉', // horizontal tab symbol
	'c': '␌', // form feed symbol
	'd': '␍', // carriage return symbol
	'e': '␊', // line feed symbol
	'f': '°', // degree symbol
	'g': '±', // plus-minus sign
	'h': '␤', // new line symbol
	'i': '␋', // vertical tab symbol
	'j': '┘', // bottom right
	'k': '┐', // top right
	'l': '┌', // top left
	'm': '└', // bottom left
	'n': '┼', // cross
	'o': '⎺', // scan line 1
	'p': '⎻', // scan line 2
	'q': '─', // scan line 3
	'r': '─', // scan line 4
	's': '⎽', // scan line 5
	't': '├', // vertical and right
	'u': '┤', // vertical and left
	'v': '┴', // horizontal and up
	'w': '┬', // horizontal and down
	'x': '│', // vertical bar
	'y': '≤', // less or equal
	'z': '≥', // greater or equal
	'{': 'π', // pi
	'|': '≠', // not equal
	'}': '£', // Pounds currency symbol
	'~': '·', // centered dot
}

type parseState struct {
	code          string
	esc           int
	escNext       bool // true when ESC was just seen; next char goes to parseEscState
	osc, apc, dcs bool
	vt100         rune
	printing      bool
}

func (t *Terminal) handleOutput(buf []byte) []byte {
	if t.hasSelectedText() {
		t.clearSelectedText()
	}
	if t.state == nil {
		t.state = &parseState{
			esc: noEscape,
		}
	}
	var (
		size int
		r    rune
		i    = -1
	)
	for {
		i += size
		buf = buf[size:]
		r, size = utf8.DecodeRune(buf)
		if size == 0 {
			break
		}
		if r == utf8.RuneError && size == 1 { // not UTF-8, or sequence split by read boundary
			if incompleteUTF8PrefixLen(buf) > 0 {
				// 多字节序列被读取边界截断：保留尾部字节，与下一块拼齐后再解码。
				// 直接丢弃会造成中文乱码，且远端光标已前进导致后续列错位。
				// 必须拷贝：leftOver 会被下一次 Read 的底层缓冲覆盖。
				tail := make([]byte, len(buf))
				copy(tail, buf)
				return tail
			}
			if !t.state.printing {
				if t.debug {
					log.Println("Invalid UTF-8", buf[0])
				}
				continue
			}
		}

		if t.state.printing {
			t.parsePrinting(buf, size)
			continue
		}
		if r == asciiEscape {
			t.state.esc = i
			t.state.escNext = true
			continue
		}
		if t.state.dcs {
			t.parseDCS(r)
			continue
		}
		if t.state.escNext {
			t.state.escNext = false
			if cont := t.parseEscState(r); cont {
				continue
			}
			t.state.esc = noEscape
			continue
		}
		if t.state.apc {
			t.parseAPC(r)
			continue
		}
		if t.state.osc {
			t.parseOSC(r)
			continue
		} else if t.state.vt100 != 0 {
			t.handleVT100(string([]rune{t.state.vt100, r}))
			t.state.vt100 = 0
			continue
		} else if t.state.esc != noEscape {
			t.parseEscape(r)
			continue
		}

		if out, ok := specialChars[r]; ok {
			if out == nil {
				continue
			}
			t.ui(func() { out(t) })
		} else {
			// check to see which charset to use
			if t.useG1CharSet {
				chr := charSetMap[t.g1Charset](r)
				t.ui(func() { t.handleOutputChar(chr) })
			} else {
				chr := charSetMap[t.g0Charset](r)
				t.ui(func() { t.handleOutputChar(chr) })
			}
		}
	}

	return buf
}

// incompleteUTF8PrefixLen 判断 buf 开头是否为被读取边界截断的 UTF-8 序列前缀。
// 返回前缀字节数（1~3）；真正的非法字节（孤立续字节、非法首字节等）返回 0。
func incompleteUTF8PrefixLen(b []byte) int {
	if len(b) == 0 || len(b) >= utf8.UTFMax {
		return 0
	}
	var total int
	switch c := b[0]; {
	case c >= 0xC2 && c <= 0xDF:
		total = 2
	case c >= 0xE0 && c <= 0xEF:
		total = 3
	case c >= 0xF0 && c <= 0xF4:
		total = 4
	default:
		return 0
	}
	if len(b) >= total {
		return 0
	}
	for _, c := range b[1:] {
		if c < 0x80 || c > 0xBF {
			return 0
		}
	}
	return len(b)
}

func (t *Terminal) parseEscState(r rune) (shouldContinue bool) {
	switch r {
	case '[':
		return true
	case '\\':
		if t.state.osc {
			code := t.state.code
			t.ui(func() { t.handleOSC(code) })
		}
		t.state.code = ""
		t.state.osc = false
	case ']':
		t.state.osc = true
	case '(', ')':
		t.state.vt100 = r
	case '7':
		t.savedRow = t.cursorRow
		t.savedCol = t.cursorCol
	case '8':
		t.cursorRow = t.savedRow
		t.cursorCol = t.savedCol
	case 'D':
		t.scrollDown()
	case 'M':
		t.scrollUp()
	case 'P':
		t.state.dcs = true
	case '_':
		t.state.apc = true
	case '=', '>':
	}
	return false
}

func (t *Terminal) parseEscape(r rune) {
	t.state.code += string(r)
	if (r < '0' || r > '9') && r != ';' && r != '=' && r != '?' && r != '>' {
		code := t.state.code
		t.ui(func() { t.handleEscape(code) })
		t.state.code = ""
		t.state.esc = noEscape
	}
}

func (t *Terminal) parsePrinting(buf []byte, size int) {
	t.printData = append(t.printData, buf[:size]...)
	if bytes.HasSuffix(t.printData, []byte{asciiEscape, '[', '4', 'i'}) {
		// Handle the end of printing
		t.printData = t.printData[:len(t.printData)-4]
		escapePrinterMode(t, "4")
		t.state.esc = noEscape
	}
}

func (t *Terminal) parseAPC(r rune) {
	if r == 0 {
		code := t.state.code
		t.ui(func() { t.handleAPC(code) })
		t.state.code = ""
		t.state.apc = false
	} else {
		t.state.code += string(r)
	}
}

func (t *Terminal) parseOSC(r rune) {
	if r == asciiBell || r == 0 {
		code := t.state.code
		t.ui(func() { t.handleOSC(code) })
		t.state.code = ""
		t.state.osc = false
	} else {
		t.state.code += string(r)
	}
}

func (t *Terminal) parseDCS(r rune) {
	if r == '\\' {
		code := t.state.code
		t.ui(func() { t.handleDCS(code) })
		t.state.code = ""
		t.state.dcs = false
	} else {
		t.state.code += string(r)
	}
}

func (t *Terminal) handleOutputChar(r rune) {
	if t == nil || t.content == nil {
		return
	}
	// 东亚宽字符（中文/日文/韩文等）占两列：占位两格避免渲染重叠
	w := runewidth.RuneWidth(r)
	if w != 2 {
		w = 1
	}

	if t.cursorCol == int(t.config.Columns) {
		if !t.disableAutoWrap {
			t.cursorCol = 0
			handleOutputLineFeed(t)
		} else {
			// In non-wrap mode, overwrite the last character
			t.cursorCol = int(t.config.Columns) - 1
		}
	}
	// 宽字符落在末列时放不下，先换行（非自动换行模式下退化为覆盖末列）
	if w == 2 && t.cursorCol == int(t.config.Columns)-1 && !t.disableAutoWrap && t.config.Columns >= 2 {
		t.cursorCol = 0
		handleOutputLineFeed(t)
	}

	var cellStyle widget.TextGridStyle
	textStyle := fyne.TextStyle{
		Monospace: true,

		Bold:          t.bold,
		Italic:        t.italic,
		Underline:     t.underline,
		Strikethrough: t.strikethrough,
	}
	// 未指定颜色时使用终端默认黑底白字（不随系统浅色主题变为黑字）
	fg, bg := t.currentColors()
	cellStyle = &widget.CustomTextGridStyle{FGColor: fg, BGColor: bg,
		TextStyle: textStyle}
	if t.blinking {
		cellStyle = widget2.NewTermTextGridStyle(fg, bg, highlightBitMask, t.blinking, textStyle)
	}

	row, col := t.cursorRow, t.cursorCol
	// 清理宽字符被半覆盖的残影：覆盖第二半时清掉第一半；覆盖第一半时清掉第二半
	if len(t.content.Rows) > row {
		cells := t.content.Rows[row].Cells
		if col > 0 && col-1 < len(cells) && runewidth.RuneWidth(cells[col-1].Rune) == 2 {
			t.content.SetCell(row, col-1, widget.TextGridCell{Rune: ' ', Style: cellStyle})
		}
		if col < len(cells) && runewidth.RuneWidth(cells[col].Rune) == 2 && col+1 < int(t.config.Columns) {
			t.content.SetCell(row, col+1, widget.TextGridCell{Rune: ' ', Style: cellStyle})
		}
	}

	cell := widget.TextGridCell{Rune: r, Style: cellStyle}
	oldLen := 0
	if len(t.content.Rows) > row {
		oldLen = len(t.content.Rows[row].Cells)
	}
	t.content.SetCell(row, col, cell)
	if w == 2 && col+1 < int(t.config.Columns) {
		// 宽字符的续格：占位空格保证下一字符从正确列开始；
		// 背景必须透明——TextGrid 渲染器逐列绘制背景矩形，若续格带背景
		// 会按 z 序盖住宽字形溢出到该列的右半，导致中文只显示一半
		contStyle := &widget.CustomTextGridStyle{FGColor: fg, BGColor: nil, TextStyle: textStyle}
		t.content.SetCell(row, col+1, widget.TextGridCell{Rune: ' ', Style: contStyle})
	}

	for i := oldLen; i < col; i++ {
		if t.content.Rows[row].Cells[i].Rune == 0 {
			t.content.Rows[row].Cells[i].Rune = ' '
		}
	}
	t.lastChar = r
	t.cursorCol += w
}

func (t *Terminal) ringBell() {
	t.bell = true
	t.Refresh()

	go func() {
		time.Sleep(time.Millisecond * 300)
		t.bell = false
		t.ui(t.Refresh)
	}()
}

func (t *Terminal) scrollUp() {
	if t == nil || t.content == nil || t.scrollBottom < t.scrollTop {
		return
	}
	for i := t.scrollBottom; i > t.scrollTop; i-- {
		t.content.SetRow(i, t.content.Row(i-1))
	}
	if t.scrollTop >= 0 {
		t.content.SetRow(t.scrollTop, widget.TextGridRow{})
	}
	t.content.Refresh()
}

func (t *Terminal) scrollDown() {
	if t == nil || t.content == nil {
		return
	}
	// 全屏滚动区时，顶部滚出的行进入回滚缓冲（供滚轮回看）
	fullScreen := t.scrollTop == 0 && t.scrollBottom == int(t.config.Rows)-1 &&
		!t.altBufferActive
	captured := false
	if fullScreen && t.scrollTop >= 0 && t.scrollTop < len(t.content.Rows) {
		t.pushScrollback(t.content.Rows[t.scrollTop])
		captured = true
	}

	i := t.scrollTop
	if i < 0 {
		i = 0
	}
	for ; i < t.scrollBottom && i < len(t.content.Rows)-1; i++ {
		t.content.SetRow(i, t.content.Row(i+1))
	}
	if t.scrollBottom >= 0 {
		t.content.SetRow(t.scrollBottom, widget.TextGridRow{})
	}
	if captured && t.scrollOffset > 0 {
		t.rebuildView() // 用户处于回看状态：保持视窗锚定，内容下移
	}
	t.content.Refresh()
}

func handleOutputBackspace(t *Terminal) {
	if t == nil || t.content == nil {
		return
	}
	row := t.content.Row(t.cursorRow)
	if len(row.Cells) == 0 {
		return
	}
	t.moveCursor(t.cursorRow, t.cursorCol-1)
}

func handleOutputBell(t *Terminal) {
	t.ringBell()
}

func handleOutputCarriageReturn(t *Terminal) {
	t.moveCursor(t.cursorRow, 0)
}

func handleOutputLineFeed(t *Terminal) {
	if t.cursorRow == t.scrollBottom {
		t.scrollDown()
		if t.newLineMode {
			t.moveCursor(t.cursorRow, 0)
		}
		return
	}
	if t.newLineMode {
		t.moveCursor(t.cursorRow+1, 0)
		return
	}
	t.moveCursor(t.cursorRow+1, t.cursorCol)
}

func handleOutputTab(t *Terminal) {
	end := t.cursorCol - t.cursorCol%tabWidth + tabWidth
	for t.cursorCol < end {
		t.handleOutputChar(' ')
	}
}

func handleShiftOut(t *Terminal) {
	t.useG1CharSet = true
}

func handleShiftIn(t *Terminal) {
	t.useG1CharSet = false
}

// SetPrinterFunc sets the printer function which is executed when printing.
func (t *Terminal) SetPrinterFunc(printerFunc PrinterFunc) {
	t.printer = printerFunc
}
