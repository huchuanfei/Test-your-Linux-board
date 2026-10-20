package ui

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
)

type stubReadCloser struct {
	uri fyne.URI
}

func (s stubReadCloser) Read([]byte) (int, error) { return 0, io.EOF }
func (s stubReadCloser) Close() error             { return nil }
func (s stubReadCloser) URI() fyne.URI            { return s.uri }

// TestFileDialogResizeBeforeShowPanics 锁定闪退根因：
// Fyne v2.8.1 FileDialog.Resize 在 Show 前访问 nil dialog.win。
func TestFileDialogResizeBeforeShowPanics(t *testing.T) {
	a := test.NewApp()
	w := a.NewWindow("")
	w.Resize(fyne.NewSize(800, 600))
	fd := dialog.NewFileOpen(func(fyne.URIReadCloser, error) {}, w)
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		fd.Resize(fyne.NewSize(700, 500))
	}()
	if !panicked {
		t.Fatal("Fyne FileDialog.Resize before Show 应按 nil dialog.win panic")
	}
}

func TestOpenScriptDoesNotPanic(t *testing.T) {
	p := newTestMainPage(t)
	p.openScript()
	if p.scriptPath != "" {
		t.Fatal("仅打开对话框不应写入 scriptPath")
	}
	if p.scriptPathEntry.Text != "" {
		t.Fatalf("取消前路径框被改写: %q", p.scriptPathEntry.Text)
	}
}

func TestApplyOpenedScriptCancel(t *testing.T) {
	p := newTestMainPage(t)
	p.applyOpenedScript(nil, nil)
	if p.scriptPath != "" {
		t.Fatal("取消选择不应设置脚本路径")
	}
}

func TestApplyOpenedScriptError(t *testing.T) {
	p := newTestMainPage(t)
	p.applyOpenedScript(nil, errors.New("open failed"))
	if p.scriptPath != "" {
		t.Fatal("打开失败不应设置脚本路径")
	}
}

func TestApplyOpenedScriptRejectsNonSh(t *testing.T) {
	p := newTestMainPage(t)
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := storage.Reader(storage.NewFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	p.applyOpenedScript(r, nil)
	if p.scriptPath != "" {
		t.Fatal("非 .sh 不应被接受")
	}
}

func TestApplyOpenedScriptReadFail(t *testing.T) {
	p := newTestMainPage(t)
	missing := filepath.Join(t.TempDir(), "gone.sh")
	p.applyOpenedScript(stubReadCloser{uri: storage.NewFileURI(missing)}, nil)
	if p.scriptPath != "" {
		t.Fatal("读文件失败不应设置脚本路径")
	}
}

func TestApplyOpenedScriptNilURI(t *testing.T) {
	p := newTestMainPage(t)
	p.applyOpenedScript(stubReadCloser{}, nil)
	if p.scriptPath != "" {
		t.Fatal("nil URI 不应设置路径")
	}
}

func TestApplyOpenedScriptSetsPath(t *testing.T) {
	p := newTestMainPage(t)
	path := filepath.Join(t.TempDir(), "board.sh")
	if err := os.WriteFile(path, []byte("echo ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := storage.Reader(storage.NewFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	p.applyOpenedScript(r, nil)
	if p.scriptPath != path {
		t.Fatalf("scriptPath=%q want %q", p.scriptPath, path)
	}
	if p.scriptPathEntry.Text != path {
		t.Fatalf("pathEntry=%q want %q", p.scriptPathEntry.Text, path)
	}
}

func TestScriptPathEntryReadOnlyKeepsFullPath(t *testing.T) {
	p := newTestMainPage(t)
	path := filepath.Join(t.TempDir(), "keep.sh")
	if err := os.WriteFile(path, []byte("echo ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := storage.Reader(storage.NewFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	p.applyOpenedScript(r, nil)
	p.scriptPathEntry.SetText("echo should not stick")
	if p.scriptPathEntry.Text != path {
		t.Fatalf("只读路径被改写: %q", p.scriptPathEntry.Text)
	}
	if p.scriptPath != path {
		t.Fatalf("scriptPath=%q", p.scriptPath)
	}
}

func TestDefaultScriptStartDir(t *testing.T) {
	if defaultScriptStartDir() == nil {
		t.Fatal("工作目录或 scripts 应可作为起始目录")
	}
}
