package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// actionButtonLabel 动作按钮最小宽度基准（「开始测试」四字）。
const actionButtonLabel = "开始测试"

// actionButtonMinSize 以「开始测试」绿按钮为基准的最小尺寸。
func actionButtonMinSize() fyne.Size {
	return newGreenButton(actionButtonLabel, nil).MinSize()
}

// wrapActionWidth 把按钮包成与「开始测试」同最小宽度，避免「连接」/「断开」变窄。
func wrapActionWidth(btn *widget.Button) fyne.CanvasObject {
	sz := actionButtonMinSize()
	if btn != nil {
		own := btn.MinSize()
		if own.Height > sz.Height {
			sz.Height = own.Height
		}
	}
	return container.NewGridWrap(sz, btn)
}

// 统一按钮绿：深一点保证白字对比度（覆盖默认 Success 的亮绿+深字）
var (
	buttonGreen    = color.NRGBA{R: 0x2e, G: 0x7d, B: 0x32, A: 0xff}
	buttonGreenFg  = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	buttonGreenOff = color.NRGBA{R: 0x81, G: 0xc7, B: 0x84, A: 0xff}
)

// applyGreenButton 把已有按钮统一成绿底白字（SuccessImportance + 主题色）
func applyGreenButton(btn *widget.Button) *widget.Button {
	if btn == nil {
		return nil
	}
	btn.Importance = widget.SuccessImportance
	return btn
}

// newGreenButton 绿底白字按钮，项目内按钮统一走这里，避免各处复制配色。
func newGreenButton(label string, tapped func()) *widget.Button {
	return applyGreenButton(widget.NewButton(label, tapped))
}

// ThemeButtonColor 供主窗口主题查询统一绿色按钮色。
func ThemeButtonColor(n fyne.ThemeColorName) (color.Color, bool) {
	switch n {
	case theme.ColorNameSuccess, theme.ColorNamePrimary, theme.ColorNameButton:
		return buttonGreen, true
	case theme.ColorNameForegroundOnSuccess, theme.ColorNameForegroundOnPrimary:
		return buttonGreenFg, true
	case theme.ColorNameDisabledButton:
		return buttonGreenOff, true
	default:
		return nil, false
	}
}
