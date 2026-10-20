package ui

import (
	"testing"

	"fyne.io/fyne/v2/widget"
)

func TestAppTitleExact(t *testing.T) {
	want := "Test your Linux board"
	if AppTitle != want {
		t.Fatalf("AppTitle=%q", AppTitle)
	}
}

func TestGreenButtonImportance(t *testing.T) {
	btn := newGreenButton("确定", nil)
	if btn.Importance != widget.SuccessImportance {
		t.Fatalf("Importance=%v", btn.Importance)
	}
}

func TestWrapActionWidthMatchesStartLabel(t *testing.T) {
	start := wrapActionWidth(newGreenButton(actionButtonLabel, nil))
	conn := wrapActionWidth(newGreenButton(connBtnConnect, nil))
	disc := wrapActionWidth(newGreenButton(connBtnDisconnect, nil))
	sw, cw, dw := start.MinSize().Width, conn.MinSize().Width, disc.MinSize().Width
	if cw != sw || dw != sw {
		t.Fatalf("连接/断开包装宽应与开始测试一致: start=%v conn=%v disc=%v", sw, cw, dw)
	}
}
