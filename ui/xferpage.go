package ui

import (
	"fmt"
	"os"
	"path/filepath"

	"boardtest/engine"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

// TransferPage 主机 ↔ 目标板文件传输（复用测试主页 SSH 连接）
type TransferPage struct {
	win fyne.Window
	hub *engine.Hub

	localEntry   *widget.Entry
	remoteEntry  *widget.Entry
	status       *widget.Label
	btnBrowse    *widget.Button
	btnBrowseDir *widget.Button
	btnUpload    *widget.Button
	btnDownload  *widget.Button
	busy         bool

	root fyne.CanvasObject
}

// NewTransferPage 构建文件传输页
func NewTransferPage(w fyne.Window, hub *engine.Hub) fyne.CanvasObject {
	return newTransferPage(w, hub).root
}

func newTransferPage(w fyne.Window, hub *engine.Hub) *TransferPage {
	if hub == nil {
		hub = engine.NewHub()
	}
	p := &TransferPage{win: w, hub: hub}

	p.localEntry = widget.NewEntry()
	p.localEntry.SetPlaceHolder("本机文件或文件夹路径")
	p.remoteEntry = widget.NewEntry()
	p.remoteEntry.SetPlaceHolder("目标板文件或文件夹路径，如 /tmp/app.bin 或 /tmp/dir")

	p.btnBrowse = newGreenButton("浏览文件", p.browseLocal)
	p.btnBrowseDir = newGreenButton("浏览文件夹", p.browseLocalDir)
	p.btnUpload = newGreenButton("上传", p.upload)
	p.btnDownload = newGreenButton("下载", p.download)
	p.status = widget.NewLabel("未连接：请先在测试主页进行连接测试")

	browseBtns := container.NewHBox(p.btnBrowse, p.btnBrowseDir)
	localRow := container.NewBorder(nil, nil, widget.NewLabel("本机路径:"), browseBtns, p.localEntry)
	remoteRow := container.NewBorder(nil, nil, widget.NewLabel("远端路径:"), nil, p.remoteEntry)
	btns := container.NewHBox(p.btnUpload, p.btnDownload)
	form := container.NewVBox(
		widget.NewLabel("在主机与目标板之间传输文件或文件夹（SFTP，共用测试主页连接）"),
		localRow,
		remoteRow,
		btns,
		p.status,
	)
	p.root = container.NewPadded(form)

	hub.Subscribe(func(ok bool) {
		fyne.Do(func() {
			defer func() { _ = recover() }()
			p.refreshStatus(ok)
		})
	})
	return p
}

func (p *TransferPage) refreshStatus(ok bool) {
	if p.busy {
		return
	}
	if ok {
		p.status.SetText("已连接，可以传输")
	} else {
		p.status.SetText("未连接：请先在测试主页进行连接测试")
	}
}

func (p *TransferPage) requireClient() (*engine.Client, bool) {
	if p.hub == nil || !p.hub.Connected() {
		if p.win != nil {
			dialog.ShowError(fmt.Errorf("未连接：请先在测试主页进行连接测试"), p.win)
		}
		p.status.SetText("未连接：请先在测试主页进行连接测试")
		return nil, false
	}
	c := p.hub.Client()
	if c == nil {
		if p.win != nil {
			dialog.ShowError(fmt.Errorf("未连接：请先在测试主页进行连接测试"), p.win)
		}
		return nil, false
	}
	return c, true
}

func (p *TransferPage) browseLocal() {
	defer func() {
		if rec := recover(); rec != nil && p.win != nil {
			dialog.ShowError(fmt.Errorf("打开文件失败: %v", rec), p.win)
		}
	}()
	if p.win == nil {
		return
	}
	fd := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, p.win)
			return
		}
		if r == nil {
			return
		}
		defer r.Close()
		if uri := r.URI(); uri != nil {
			p.localEntry.SetText(uri.Path())
		}
	}, p.win)
	if wd, err := os.Getwd(); err == nil {
		if list, err := storage.ListerForURI(storage.NewFileURI(wd)); err == nil {
			fd.SetLocation(list)
		}
	}
	fd.Show()
	fd.Resize(fyne.NewSize(700, 500))
}

// browseLocalDir 选择本机文件夹（上传整个目录时使用）
func (p *TransferPage) browseLocalDir() {
	defer func() {
		if rec := recover(); rec != nil && p.win != nil {
			dialog.ShowError(fmt.Errorf("打开文件夹失败: %v", rec), p.win)
		}
	}()
	if p.win == nil {
		return
	}
	fd := dialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, p.win)
			return
		}
		if lu == nil {
			return
		}
		p.localEntry.SetText(lu.Path())
	}, p.win)
	if wd, err := os.Getwd(); err == nil {
		if list, err := storage.ListerForURI(storage.NewFileURI(wd)); err == nil {
			fd.SetLocation(list)
		}
	}
	fd.Show()
	fd.Resize(fyne.NewSize(700, 500))
}

func (p *TransferPage) upload() {
	p.startXfer(true)
}

func (p *TransferPage) download() {
	p.startXfer(false)
}

func (p *TransferPage) startXfer(upload bool) {
	defer func() {
		if rec := recover(); rec != nil {
			p.busy = false
			p.setButtonsEnabled(true)
			if p.win != nil {
				dialog.ShowError(fmt.Errorf("传输异常: %v", rec), p.win)
			}
		}
	}()
	if p.busy {
		return
	}
	client, ok := p.requireClient()
	if !ok {
		return
	}
	local := p.localEntry.Text
	remote := p.remoteEntry.Text
	if local == "" || remote == "" {
		if p.win != nil {
			dialog.ShowError(fmt.Errorf("请填写本机路径和远端路径"), p.win)
		}
		return
	}

	p.busy = true
	p.setButtonsEnabled(false)
	if upload {
		p.status.SetText("正在上传...")
	} else {
		p.status.SetText("正在下载...")
	}

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				p.uiDone(fmt.Errorf("传输异常: %v", rec), "")
			}
		}()
		var err error
		progress := func(written, total int64) {
			fyne.Do(func() {
				if total > 0 {
					p.status.SetText(fmt.Sprintf("传输中 %d / %d", written, total))
				} else {
					p.status.SetText(fmt.Sprintf("传输中 %d 字节", written))
				}
			})
		}
		if upload {
			err = client.Upload(local, remote, progress)
		} else {
			err = client.Download(remote, local, progress)
		}
		if err != nil {
			p.uiDone(err, "")
			return
		}
		if upload {
			p.uiDone(nil, "上传完成: "+filepath.Base(local))
		} else {
			p.uiDone(nil, "下载完成: "+filepath.Base(local))
		}
	}()
}

func (p *TransferPage) uiDone(err error, okMsg string) {
	fyne.Do(func() {
		p.busy = false
		p.setButtonsEnabled(true)
		if err != nil {
			p.status.SetText("失败: " + err.Error())
			if p.win != nil {
				dialog.ShowError(err, p.win)
			}
			return
		}
		p.status.SetText(okMsg)
	})
}

func (p *TransferPage) setButtonsEnabled(on bool) {
	if p.btnBrowse == nil {
		return
	}
	if on {
		p.btnBrowse.Enable()
		p.btnBrowseDir.Enable()
		p.btnUpload.Enable()
		p.btnDownload.Enable()
	} else {
		p.btnBrowse.Disable()
		p.btnBrowseDir.Disable()
		p.btnUpload.Disable()
		p.btnDownload.Disable()
	}
}
