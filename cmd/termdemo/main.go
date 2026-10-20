// termdemo 终端渲染临时验证程序（验证后删除）：
// 本地shell + 自动注入中文/大量输出，配合Xephyr截图验证中文渲染、回滚缓冲与滚动条。
package main

import (
	"fmt"
	"image/color"
	"time"

	terminal "boardtest/internal/term"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

func main() {
	a := app.New()
	w := a.NewWindow("终端验证")
	t := terminal.New()

	bg := canvas.NewRectangle(color.Black)
	w.SetContent(container.NewStack(bg, t))
	w.Resize(fyne.NewSize(900, 560))
	w.Show()

	go func() {
		_ = t.RunLocalShell()
	}()

	// 等 shell 就绪后自动输入：中文混排 + 超过一屏的输出（撑出回滚缓冲）
	go func() {
		time.Sleep(2 * time.Second)
		_, _ = t.Write([]byte("echo \"中文测试：嵌入式板卡生产测试工具 OK\"\n"))
		time.Sleep(500 * time.Millisecond)
		_, _ = t.Write([]byte("for i in $(seq 1 40); do echo \"第${i}行 历史数据滚动测试\"; done\n"))
		time.Sleep(500 * time.Millisecond)
		_, _ = t.Write([]byte("echo \"完成：终端就绪\"\n"))
		fmt.Println("demo commands injected")
	}()

	a.Run()
}
