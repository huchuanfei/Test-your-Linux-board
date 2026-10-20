package terminal

import (
	"fmt"
	"runtime/debug"
	"testing"

	widget2 "boardtest/internal/term/internal/widget"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func mustNoPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panic: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
}

// typicalBoardShell 模拟板卡 ash/busybox 登录后立刻下发的 CSI（成功连上才会有）。
var typicalBoardShell = []byte(
	"\x1b[?2004h\x1b[?25h\x1b[2J\x1b[H\x1b[1;1H" +
		"\x1b[0m\x1b[1;32mroot@board\x1b[0m:# \r\n" +
		"\x1b[6n\x1b[@\x1b[M\x1b[T" +
		"Welcome\r\n",
)

func TestHandleOutputWithoutRendererNoPanic(t *testing.T) {
	_ = test.NewApp()
	term := New()
	mustNoPanic(t, "nil content + CSI", func() {
		_ = term.handleOutput(typicalBoardShell)
	})
	mustNoPanic(t, "nil content + text", func() {
		_ = term.handleOutput([]byte("hello\nworld\n"))
	})
}

func TestHandleOutputZeroSizeNoPanic(t *testing.T) {
	a := test.NewApp()
	w := a.NewWindow("")
	term := New()
	w.SetContent(term)
	w.Resize(fyne.NewSize(0, 0))
	term.Resize(fyne.NewSize(0, 0))
	term.Refresh()
	mustNoPanic(t, "rows=0 + CSI", func() {
		_ = term.handleOutput(typicalBoardShell)
	})
	mustNoPanic(t, "rows=0 + scroll", func() {
		for i := 0; i < 40; i++ {
			_ = term.handleOutput([]byte(fmt.Sprintf("line %d\r\n", i)))
		}
	})
}

func TestHandleOutputShownThenHiddenNoPanic(t *testing.T) {
	a := test.NewApp()
	w := a.NewWindow("")
	term := New()
	w.SetContent(term)
	w.Resize(fyne.NewSize(800, 600))
	term.Refresh()
	term.Hide()
	term.Resize(fyne.NewSize(0, 0))
	mustNoPanic(t, "hidden zero + CSI", func() {
		_ = term.handleOutput(typicalBoardShell)
	})
}

func TestEscapeInsertCharsEmptyRowsNoPanic(t *testing.T) {
	_ = test.NewApp()
	term := New()
	term.content = widget2.NewTermGrid()
	mustNoPanic(t, "insert chars empty rows", func() {
		escapeInsertChars(term, "3")
	})
}

func TestScrollDownRowsZeroNoPanic(t *testing.T) {
	_ = test.NewApp()
	term := New()
	term.content = widget2.NewTermGrid()
	term.config.Rows = 0
	term.scrollBottom = -1
	mustNoPanic(t, "scrollDown rows=0", func() {
		term.scrollDown()
		term.scrollUp()
		handleOutputLineFeed(term)
	})
}
