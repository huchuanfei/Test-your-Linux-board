package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadDownloadRoundTrip(t *testing.T) {
	cfg := startTestSSHServer(t, "sftp")
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer client.Close()

	dir := t.TempDir()
	local := filepath.Join(dir, "src.bin")
	payload := []byte("board-xfer-ok-42")
	if err := os.WriteFile(local, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(dir, "remote", "dst.bin") // sftp 服务落在本机文件系统
	gotProg := false
	if err := client.UploadFile(local, remote, func(w, total int64) {
		if w > 0 {
			gotProg = true
		}
	}); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if !gotProg {
		t.Fatal("上传应汇报进度")
	}

	out := filepath.Join(dir, "out.bin")
	if err := client.DownloadFile(remote, out, nil); err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(payload) {
		t.Fatalf("内容不符: %q", body)
	}
}

func TestUploadRequiresConnection(t *testing.T) {
	var c *Client
	if err := c.UploadFile("/tmp/a", "/tmp/b", nil); err == nil {
		t.Fatal("未连接应失败")
	}
	c = &Client{}
	if err := c.DownloadFile("/tmp/a", "/tmp/b", nil); err == nil {
		t.Fatal("空 Client 下载应失败")
	}
}

func TestUploadRejectsEmptyPath(t *testing.T) {
	cfg := startTestSSHServer(t, "sftp")
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer client.Close()
	if err := client.UploadFile("", "/tmp/x", nil); err == nil {
		t.Fatal("空本机路径应失败")
	}
	if err := client.DownloadFile("", "/tmp/x", nil); err == nil {
		t.Fatal("空远端路径应失败")
	}
}

// TestUploadDownloadDirectory 目录递归上传/下载：多级子目录与文件内容保持一致
func TestUploadDownloadDirectory(t *testing.T) {
	cfg := startTestSSHServer(t, "sftp")
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer client.Close()

	base := t.TempDir()
	local := filepath.Join(base, "tree")
	if err := os.MkdirAll(filepath.Join(local, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "sub", "b.txt"), []byte("bbbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	remote := filepath.Join(base, "remote-tree")
	var uploaded int64
	if err := client.Upload(local, remote, func(w, total int64) { uploaded = w }); err != nil {
		t.Fatalf("上传目录失败: %v", err)
	}
	if uploaded == 0 {
		t.Fatal("目录上传应汇报累计进度")
	}
	for _, rel := range []string{"a.txt", filepath.Join("sub", "b.txt")} {
		if _, err := os.Stat(filepath.Join(remote, rel)); err != nil {
			t.Fatalf("远端缺少 %s: %v", rel, err)
		}
	}

	out := filepath.Join(base, "out-tree")
	if err := client.Download(remote, out, nil); err != nil {
		t.Fatalf("下载目录失败: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(out, "sub", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "bbbb" {
		t.Fatalf("下载内容不符: %q", body)
	}
}

// TestUploadDownloadSingleFileUnified 统一入口传单文件：仍按完整文件路径处理
func TestUploadDownloadSingleFileUnified(t *testing.T) {
	cfg := startTestSSHServer(t, "sftp")
	client, err := Dial(cfg, 5*time.Second)
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer client.Close()

	base := t.TempDir()
	local := filepath.Join(base, "one.bin")
	if err := os.WriteFile(local, []byte("xyz"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(base, "remote-one.bin")
	if err := client.Upload(local, remote, nil); err != nil {
		t.Fatalf("单文件上传失败: %v", err)
	}
	out := filepath.Join(base, "out-one.bin")
	if err := client.Download(remote, out, nil); err != nil {
		t.Fatalf("单文件下载失败: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "xyz" {
		t.Fatalf("单文件内容不符: %q", body)
	}
}
