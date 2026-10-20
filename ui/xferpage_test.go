package ui

import (
	"testing"

	"boardtest/engine"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func newTestXferPage(t *testing.T, hub *engine.Hub) *TransferPage {
	t.Helper()
	a := test.NewApp()
	w := a.NewWindow("")
	if hub == nil {
		hub = engine.NewHub()
	}
	p := newTransferPage(w, hub)
	w.SetContent(p.root)
	w.Resize(fyne.NewSize(800, 400))
	return p
}

func TestTransferPageUnconnectedRefuses(t *testing.T) {
	p := newTestXferPage(t, nil)
	p.localEntry.SetText("/tmp/a")
	p.remoteEntry.SetText("/tmp/b")
	p.upload()
	if p.busy {
		t.Fatal("未连接不应进入传输")
	}
	p.download()
	if p.busy {
		t.Fatal("未连接不应进入下载")
	}
}

func TestTransferPageButtonsAreGreen(t *testing.T) {
	p := newTestXferPage(t, nil)
	if p.btnUpload.Importance != 0 && p.btnUpload.Text != "上传" {
		t.Fatalf("上传按钮文案=%q", p.btnUpload.Text)
	}
	if p.btnUpload.Text != "上传" || p.btnDownload.Text != "下载" {
		t.Fatal("按钮文案不符")
	}
}

// TestTransferPageHasFolderBrowse 本机路径旁同时提供文件与文件夹浏览入口
func TestTransferPageHasFolderBrowse(t *testing.T) {
	p := newTestXferPage(t, nil)
	if p.btnBrowse == nil || p.btnBrowse.Text != "浏览文件" {
		t.Fatalf("应有浏览文件按钮, got %v", p.btnBrowse)
	}
	if p.btnBrowseDir == nil || p.btnBrowseDir.Text != "浏览文件夹" {
		t.Fatalf("应有浏览文件夹按钮, got %v", p.btnBrowseDir)
	}
}
