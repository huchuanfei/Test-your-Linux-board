package terminal

import (
	"testing"
	"time"

	widget2 "boardtest/internal/term/internal/widget"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// newOutputTestTerm 构造可写屏的最小终端（80x24 网格）
func newOutputTestTerm() *Terminal {
	_ = test.NewApp()
	term := New()
	term.content = widget2.NewTermGrid()
	term.content.CreateRenderer() // 初始化 TextGrid.content，否则 SetCell 内部 refreshCell 空指针
	term.config.Columns = 80
	term.config.Rows = 24
	term.scrollBottom = 23
	return term
}

// waitCells 轮询等待第 0 行写出 n 个单元格（handleOutput 经 fyne.Do 异步写屏）。
func waitCells(t *testing.T, term *Terminal, n int) []widget.TextGridCell {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(term.content.Rows) > 0 && len(term.content.Rows[0].Cells) >= n {
			return term.content.Rows[0].Cells
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待 %d 个单元格超时", n)
	return nil
}

// TestHandleOutputSplitUTF8 中文三字节序列跨读取边界时不得丢字节：
// 第一次调用返回未消费的前缀，拼接后残余为空（写屏见 handleOutputChar 直测）。
func TestHandleOutputSplitUTF8(t *testing.T) {
	term := newOutputTestTerm()

	zh := []byte("中") // E4 B8 AD
	left := term.handleOutput(zh[:2])
	if len(left) != 2 || left[0] != zh[0] || left[1] != zh[1] {
		t.Fatalf("截断前缀应原样保留，got %v", left)
	}

	left = term.handleOutput(append(left, zh[2]))
	if len(left) != 0 {
		t.Fatalf("拼接后不应有残留，got %v", left)
	}
}

// TestHandleOutputSplitUTF8EndToEnd 完整链路：分两块喂入，最终写出宽字符+续格。
func TestHandleOutputSplitUTF8EndToEnd(t *testing.T) {
	term := newOutputTestTerm()

	zh := []byte("中")
	left := term.handleOutput(zh[:1])
	_ = term.handleOutput(append(left, zh[1:]...)) // 模拟 run() 的 leftover 拼接

	cells := waitCells(t, term, 2)
	if cells[0].Rune != '中' || cells[1].Rune != ' ' {
		t.Fatalf("应写出宽字符+续格占位，got %q", string([]rune{cells[0].Rune, cells[1].Rune}))
	}
}

// TestHandleOutputInvalidUTF8Skipped 孤立续字节属真正非法输入，仍按原逻辑跳过。
func TestHandleOutputInvalidUTF8Skipped(t *testing.T) {
	term := newOutputTestTerm()

	left := term.handleOutput([]byte{0x80, 'A'})
	if len(left) != 0 {
		t.Fatalf("非法字节不应被当作截断前缀保留，got %v", left)
	}

	cells := waitCells(t, term, 1)
	if cells[0].Rune != 'A' {
		t.Fatalf("0x80 应被丢弃、'A' 应写出，got %q", string(cells[0].Rune))
	}
}

// TestIncompleteUTF8PrefixLen 前缀判定表
func TestIncompleteUTF8PrefixLen(t *testing.T) {
	cases := []struct {
		in   []byte
		want int
	}{
		{[]byte{0xE4}, 1},             // 三字节序列只来了首字节
		{[]byte{0xE4, 0xB8}, 2},       // 来了前两个
		{[]byte{0xC2}, 1},             // 两字节序列首字节
		{[]byte{0xF0, 0x9F, 0x98}, 3}, // 四字节序列前三字节
		{[]byte{0xE4, 0x41}, 0},       // 续字节位置出现非续字节：非法
		{[]byte{0x80}, 0},             // 孤立续字节：非法
		{[]byte{0xFF}, 0},             // 非法首字节
		{[]byte("中"), 0},              // 完整序列：不算前缀
		{[]byte("ab"), 0},             // 纯 ASCII
	}
	for i, c := range cases {
		if got := incompleteUTF8PrefixLen(c.in); got != c.want {
			t.Errorf("case %d: incompleteUTF8PrefixLen(% x)=%d, want %d", i, c.in, got, c.want)
		}
	}
}

// TestWideCharContinuationTransparent 宽字符续格背景必须透明，
// 否则后续列的背景矩形会盖住字形右半（中文只显示一半的直接原因）。
func TestWideCharContinuationTransparent(t *testing.T) {
	term := newOutputTestTerm()
	term.handleOutputChar('中') // 直调，绕过 fyne.Do，同步可断言

	cells := term.content.Rows[0].Cells
	if len(cells) != 2 || cells[0].Rune != '中' || cells[1].Rune != ' ' {
		t.Fatalf("应写出宽字符+续格占位，got %d cells", len(cells))
	}
	if term.cursorCol != 2 {
		t.Fatalf("宽字符应前进两列，got cursorCol=%d", term.cursorCol)
	}
	st, ok := cells[1].Style.(*widget.CustomTextGridStyle)
	if !ok {
		t.Fatalf("续格应为 CustomTextGridStyle，got %T", cells[1].Style)
	}
	if st.BGColor != nil {
		t.Fatalf("续格背景必须为 nil（透明），got %v", st.BGColor)
	}
}
