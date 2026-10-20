package engine

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/pkg/sftp"
)

// ProgressFunc 传输进度：已传输字节 / 总字节（总字节未知时为 0）
type ProgressFunc func(written, total int64)

// Upload 本机文件或文件夹 → 目标板路径（SFTP）。
// 源为文件夹时递归上传其内容到 remotePath 目录；源为文件时 remotePath 为远端完整文件路径。
func (c *Client) Upload(localPath, remotePath string, progress ProgressFunc) error {
	if c == nil || c.ssh == nil {
		return fmt.Errorf("SSH连接未建立")
	}
	localPath = filepath.Clean(localPath)
	remotePath = path.Clean(remotePath)
	if localPath == "" || localPath == "." {
		return fmt.Errorf("请填写本机路径")
	}
	if remotePath == "" || remotePath == "." {
		return fmt.Errorf("请填写远端路径")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("读取本机路径失败: %w", err)
	}
	sc, err := sftp.NewClient(c.ssh)
	if err != nil {
		return fmt.Errorf("SFTP不可用: %w", err)
	}
	defer sc.Close()
	if info.IsDir() {
		return uploadTree(sc, localPath, remotePath, progress)
	}
	return uploadFile(sc, localPath, remotePath, progress)
}

// Download 目标板文件或文件夹 → 本机路径（SFTP）。
// 源为远端文件夹时递归下载其内容到 localPath 目录；源为文件时 localPath 为本地完整文件路径。
func (c *Client) Download(remotePath, localPath string, progress ProgressFunc) error {
	if c == nil || c.ssh == nil {
		return fmt.Errorf("SSH连接未建立")
	}
	remotePath = path.Clean(remotePath)
	localPath = filepath.Clean(localPath)
	if remotePath == "" || remotePath == "." {
		return fmt.Errorf("请填写远端路径")
	}
	if localPath == "" || localPath == "." {
		return fmt.Errorf("请填写本机保存路径")
	}
	sc, err := sftp.NewClient(c.ssh)
	if err != nil {
		return fmt.Errorf("SFTP不可用: %w", err)
	}
	defer sc.Close()
	st, err := sc.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("读取远端路径失败: %w", err)
	}
	if st.IsDir() {
		return downloadTree(sc, remotePath, localPath, progress)
	}
	return downloadFile(sc, remotePath, localPath, progress)
}

// UploadFile 本机文件 → 目标板路径（SFTP）。remotePath 为远端完整文件路径。
func (c *Client) UploadFile(localPath, remotePath string, progress ProgressFunc) error {
	if c == nil || c.ssh == nil {
		return fmt.Errorf("SSH连接未建立")
	}
	sc, err := sftp.NewClient(c.ssh)
	if err != nil {
		return fmt.Errorf("SFTP不可用: %w", err)
	}
	defer sc.Close()
	return uploadFile(sc, localPath, remotePath, progress)
}

// DownloadFile 目标板文件 → 本机路径（SFTP）。localPath 为本地完整文件路径。
func (c *Client) DownloadFile(remotePath, localPath string, progress ProgressFunc) error {
	if c == nil || c.ssh == nil {
		return fmt.Errorf("SSH连接未建立")
	}
	sc, err := sftp.NewClient(c.ssh)
	if err != nil {
		return fmt.Errorf("SFTP不可用: %w", err)
	}
	defer sc.Close()
	return downloadFile(sc, remotePath, localPath, progress)
}

// uploadFile 单个本机文件 → 远端完整文件路径
func uploadFile(sc *sftp.Client, localPath, remotePath string, progress ProgressFunc) error {
	localPath = filepath.Clean(localPath)
	remotePath = path.Clean(remotePath)
	if localPath == "" || localPath == "." {
		return fmt.Errorf("请填写本机文件路径")
	}
	if remotePath == "" || remotePath == "." {
		return fmt.Errorf("请填写远端文件路径")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("读取本机文件失败: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("本机路径不能是目录")
	}
	if dir := path.Dir(remotePath); dir != "" && dir != "." {
		if err := sc.MkdirAll(dir); err != nil {
			return fmt.Errorf("创建远端目录失败: %w", err)
		}
	}
	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("打开本机文件失败: %w", err)
	}
	defer src.Close()
	dst, err := sc.Create(remotePath)
	if err != nil {
		return fmt.Errorf("创建远端文件失败: %w", err)
	}
	defer dst.Close()
	if _, err := copyProgress(dst, src, info.Size(), progress); err != nil {
		return fmt.Errorf("上传失败: %w", err)
	}
	return nil
}

// downloadFile 远端完整文件路径 → 单个本机文件
func downloadFile(sc *sftp.Client, remotePath, localPath string, progress ProgressFunc) error {
	remotePath = path.Clean(remotePath)
	localPath = filepath.Clean(localPath)
	if remotePath == "" || remotePath == "." {
		return fmt.Errorf("请填写远端文件路径")
	}
	if localPath == "" || localPath == "." {
		return fmt.Errorf("请填写本机保存路径")
	}
	src, err := sc.Open(remotePath)
	if err != nil {
		return fmt.Errorf("打开远端文件失败: %w", err)
	}
	defer src.Close()
	var total int64
	if st, err := src.Stat(); err == nil {
		if st.IsDir() {
			return fmt.Errorf("远端路径不能是目录")
		}
		total = st.Size()
	}
	if dir := filepath.Dir(localPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建本机目录失败: %w", err)
		}
	}
	dst, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("创建本机文件失败: %w", err)
	}
	defer dst.Close()
	if _, err := copyProgress(dst, src, total, progress); err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	return nil
}

// uploadTree 递归上传 localRoot 目录内容到远端 remoteRoot 目录
func uploadTree(sc *sftp.Client, localRoot, remoteRoot string, progress ProgressFunc) error {
	xp := &xferProgress{fn: progress}
	return filepath.Walk(localRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(localRoot, p)
		if relErr != nil {
			return relErr
		}
		remote := remoteRoot
		if rel != "." {
			remote = path.Join(remoteRoot, filepath.ToSlash(rel))
		}
		if info.IsDir() {
			if mkErr := sc.MkdirAll(remote); mkErr != nil {
				return fmt.Errorf("创建远端目录失败: %w", mkErr)
			}
			return nil
		}
		if mkErr := sc.MkdirAll(path.Dir(remote)); mkErr != nil {
			return fmt.Errorf("创建远端目录失败: %w", mkErr)
		}
		src, openErr := os.Open(p)
		if openErr != nil {
			return fmt.Errorf("打开本机文件失败: %w", openErr)
		}
		defer src.Close()
		dst, createErr := sc.Create(remote)
		if createErr != nil {
			return fmt.Errorf("创建远端文件失败: %w", createErr)
		}
		defer dst.Close()
		if cpErr := copyFileProgress(dst, src, xp); cpErr != nil {
			return fmt.Errorf("上传失败: %w", cpErr)
		}
		return nil
	})
}

// downloadTree 递归下载远端 remoteRoot 目录内容到本地 localRoot 目录
func downloadTree(sc *sftp.Client, remoteRoot, localRoot string, progress ProgressFunc) error {
	xp := &xferProgress{fn: progress}
	w := sc.Walk(remoteRoot)
	for w.Step() {
		if werr := w.Err(); werr != nil {
			return werr
		}
		p := w.Path()
		info := w.Stat()
		rel, relErr := filepath.Rel(remoteRoot, p)
		if relErr != nil {
			return relErr
		}
		local := localRoot
		if rel != "." {
			local = filepath.Join(localRoot, filepath.FromSlash(rel))
		}
		if info.IsDir() {
			if mkErr := os.MkdirAll(local, 0o755); mkErr != nil {
				return fmt.Errorf("创建本机目录失败: %w", mkErr)
			}
			continue
		}
		if mkErr := os.MkdirAll(filepath.Dir(local), 0o755); mkErr != nil {
			return fmt.Errorf("创建本机目录失败: %w", mkErr)
		}
		src, openErr := sc.Open(p)
		if openErr != nil {
			return fmt.Errorf("打开远端文件失败: %w", openErr)
		}
		defer src.Close()
		dst, createErr := os.Create(local)
		if createErr != nil {
			return fmt.Errorf("创建本机文件失败: %w", createErr)
		}
		defer dst.Close()
		if cpErr := copyFileProgress(dst, src, xp); cpErr != nil {
			return fmt.Errorf("下载失败: %w", cpErr)
		}
	}
	return nil
}

func copyProgress(dst io.Writer, src io.Reader, total int64, progress ProgressFunc) (int64, error) {
	if progress == nil {
		return io.Copy(dst, src)
	}
	cw := &countingWriter{w: dst, total: total, fn: progress}
	n, err := io.Copy(cw, src)
	progress(n, total)
	return n, err
}

// xferProgress 目录递归传输的累计进度（总字节未知，始终以 total=0 上报）
type xferProgress struct {
	written int64
	fn      ProgressFunc
}

func (x *xferProgress) add(n int64) {
	x.written += n
	if x.fn != nil {
		x.fn(x.written, 0)
	}
}

// copyFileProgress 逐块拷贝并累计进度（目录递归用，无固定总量）
func copyFileProgress(dst io.Writer, src io.Reader, xp *xferProgress) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			xp.add(int64(n))
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type countingWriter struct {
	w     io.Writer
	n     int64
	total int64
	fn    ProgressFunc
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.fn != nil {
		c.fn(c.n, c.total)
	}
	return n, err
}
