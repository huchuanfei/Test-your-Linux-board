package engine

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHConfig 板卡SSH连接配置（对应测试主页顶部输入框）
type SSHConfig struct {
	Host string // IP
	Port string // 端口（字符串形态，与UI输入框一致）
	User string // 用户名
	Pass string // 密码
}

// Validate 校验配置完整性并返回 host:port
func (c SSHConfig) Validate() (string, error) {
	if c.Host == "" {
		return "", fmt.Errorf("请填写IP地址")
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port <= 0 || port > 65535 {
		return "", fmt.Errorf("端口无效: %q", c.Port)
	}
	if c.User == "" {
		return "", fmt.Errorf("请填写用户名")
	}
	return net.JoinHostPort(c.Host, c.Port), nil
}

// Client SSH客户端包装
type Client struct {
	ssh *ssh.Client
}

// Dial 建立SSH连接（密码认证）。
// 产线内网环境：不校验主机指纹（InsecureIgnoreHostKey）。
func Dial(cfg SSHConfig, timeout time.Duration) (*Client, error) {
	addr, err := cfg.Validate()
	if err != nil {
		return nil, err
	}
	cc := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}
	conn, err := ssh.Dial("tcp", addr, cc)
	if err != nil {
		return nil, fmt.Errorf("SSH连接失败 %s: %w", addr, err)
	}
	return &Client{ssh: conn}, nil
}

// Close 关闭连接
func (c *Client) Close() error {
	if c == nil || c.ssh == nil {
		return nil
	}
	return c.ssh.Close()
}

// Wait 阻塞直到底层SSH连接断开（供Hub监视连接失效）
func (c *Client) Wait() error {
	if c == nil || c.ssh == nil {
		return nil
	}
	return c.ssh.Wait()
}

// NewSession 在当前连接上创建新的SSH会话（供SSH终端页等交互场景使用，
// 与测试执行引擎的会话相互独立）
func (c *Client) NewSession() (*ssh.Session, error) {
	if c == nil || c.ssh == nil {
		return nil, fmt.Errorf("SSH连接未建立")
	}
	return c.ssh.NewSession()
}
