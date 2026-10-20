package main

import (
	"boardtest/engine"
	"boardtest/ui"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
)

// boardTheme 在默认主题基础上：
// 1) 表头行背景透明（避免 Table 表头浅色露边）
// 2) 按钮统一绿底白字（Success/Primary/普通按钮）
type boardTheme struct {
	fyne.Theme
}

func (t boardTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameHeaderBackground {
		return color.Transparent
	}
	if c, ok := ui.ThemeButtonColor(n); ok {
		return c
	}
	return t.Theme.Color(n, v)
}

func main() {
	a := app.NewWithID("com.boardtest.tool")
	a.Settings().SetTheme(boardTheme{theme.DefaultTheme()})
	w := a.NewWindow(ui.AppTitle)
	w.Resize(fyne.NewSize(1240, 760))
	w.SetPadded(false)

	hub := engine.NewHub()
	w.SetOnClosed(func() { hub.Close() })

	tabs := container.NewAppTabs(
		container.NewTabItem("测试主页", ui.NewMainPage(w, hub)),
		container.NewTabItem("SSH终端", ui.NewTerminalPage(w, hub)),
		container.NewTabItem("文件传输", ui.NewTransferPage(w, hub)),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	w.SetContent(container.NewPadded(tabs))
	w.ShowAndRun()
}
