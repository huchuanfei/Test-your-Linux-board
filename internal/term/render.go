package terminal

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	widget2 "boardtest/internal/term/internal/widget"
)

const cursorWidth = 2

// 滚动条配色：轨道为半透明暗色（黑底终端上可见但不抢眼），滑块用主题滚动条色
var (
	scrollBarTrackColor = color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 0x40}
)

type render struct {
	term *Terminal

	track *canvas.Rectangle // 滚动条轨道
	thumb *canvas.Rectangle // 滚动条滑块
}

func (r *render) Layout(s fyne.Size) {
	if r == nil || r.term == nil {
		return
	}
	gridWidth := s.Width - scrollBarWidth
	if gridWidth < 0 {
		gridWidth = 0
	}
	gridSize := fyne.NewSize(gridWidth, s.Height)
	if r.term.content != nil {
		r.term.content.Resize(gridSize)
	}
	if r.term.view != nil {
		r.term.view.Resize(gridSize)
	}
	r.layoutScrollbar(s)
}

func (r *render) MinSize() fyne.Size {
	return fyne.NewSize(0, 0) // don't get propped open by the text cells
}

func (r *render) Refresh() {
	if r == nil || r.term == nil {
		return
	}
	r.term.ui(func() {
		r.moveCursor()
		r.term.refreshCursor()
	})

	if r.term.content == nil || r.term.view == nil {
		return
	}
	// 回看历史时切换到视图网格；回到实时屏恢复 content
	if r.term.scrollOffset > 0 {
		r.term.rebuildView()
		r.term.content.Hide()
		r.term.view.Show()
	} else {
		r.term.view.Hide()
		r.term.content.Show()
	}
	r.updateScrollbar()

	r.term.content.Refresh()
}

func (r *render) BackgroundColor() color.Color {
	return color.Transparent
}

func (r *render) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.term.content, r.term.view, r.term.cursor, r.track, r.thumb}
}

func (r *render) Destroy() {
}

// layoutScrollbar 摆放滚动条轨道与滑块（右侧边缘）
func (r *render) layoutScrollbar(s fyne.Size) {
	r.track.Resize(fyne.NewSize(scrollBarWidth, s.Height))
	r.track.Move(fyne.NewPos(s.Width-scrollBarWidth, 0))
	r.thumb.Resize(fyne.NewSize(scrollBarWidth-4, 24))
	r.thumb.Move(fyne.NewPos(s.Width-scrollBarWidth+2, 0))
	r.updateScrollbar()
}

// updateScrollbar 依据回滚缓冲与偏移更新滚动条可见性与滑块位置
func (r *render) updateScrollbar() {
	top, thumbH, ok := r.term.scrollbarMetrics()
	s := r.term.Size()
	if !ok { // 无历史内容：隐藏滚动条
		r.track.Hide()
		r.thumb.Hide()
		return
	}
	r.track.Show()
	r.thumb.Show()
	r.track.Resize(fyne.NewSize(scrollBarWidth, s.Height))
	r.track.Move(fyne.NewPos(s.Width-scrollBarWidth, 0))
	r.thumb.Resize(fyne.NewSize(scrollBarWidth-4, thumbH))
	r.thumb.Move(fyne.NewPos(s.Width-scrollBarWidth+2, top))
}

func (r *render) moveCursor() {
	cell := r.term.guessCellSize()
	r.term.cursor.Move(fyne.NewPos(cell.Width*float32(r.term.cursorCol), cell.Height*float32(r.term.cursorRow)))
}

func (t *Terminal) refreshCursor() {
	if t == nil || t.cursor == nil {
		return
	}
	// 回看历史时活动屏被隐藏，光标随之隐藏
	t.cursor.Hidden = !t.focused || t.cursorHidden || t.scrollOffset > 0
	if t.bell {
		t.cursor.FillColor = theme.Color(theme.ColorNameError)
	} else {
		t.cursor.FillColor = theme.Color(theme.ColorNamePrimary)
	}
	t.cursor.Resize(fyne.NewSize(cursorWidth, t.guessCellSize().Height))
	t.cursor.Refresh()
}

// CreateRenderer requests a new renderer for this terminal (just a wrapper around the TextGrid)
func (t *Terminal) CreateRenderer() fyne.WidgetRenderer {
	t.ExtendBaseWidget(t)

	t.content = widget2.NewTermGrid()
	t.view = widget2.NewTermGrid()
	t.view.Hide()
	t.setupShortcuts()

	t.cursor = canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	t.cursor.Hidden = true
	t.cursor.Resize(fyne.NewSize(cursorWidth, t.guessCellSize().Height))

	r := &render{
		term:  t,
		track: canvas.NewRectangle(scrollBarTrackColor),
		thumb: canvas.NewRectangle(theme.Color(theme.ColorNameScrollBar)),
	}
	r.track.Hide()
	r.thumb.Hide()
	r.thumb.CornerRadius = (scrollBarWidth - 4) / 2
	t.cursorMoved = r.moveCursor
	return r
}
