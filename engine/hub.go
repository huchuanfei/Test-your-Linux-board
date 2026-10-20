package engine

import "sync"

// ConnListener 连接状态变化回调（true=已建立，false=已断开）。
// 可能在任意 goroutine 触发，UI 侧须自行派发到主线程。
type ConnListener func(connected bool)

// Hub 跨页面共享的 SSH 连接持有者。
// 测试主页「连接测试」成功后注入，终端页/文件传输页只复用，不各自 Dial。
type Hub struct {
	mu        sync.Mutex
	client    *Client
	gen       uint64
	listeners []ConnListener
}

// NewHub 创建空的连接持有者
func NewHub() *Hub {
	return &Hub{}
}

// Client 返回当前共享连接（可能为 nil，调用方勿 Close）
func (h *Hub) Client() *Client {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.client
}

// Connected 是否持有可用连接
func (h *Hub) Connected() bool {
	return h.Client() != nil
}

// Set 替换共享连接：关闭旧连接、监视新连接断开、通知订阅者。
// c 为 nil 等价于 Close。
func (h *Hub) Set(c *Client) {
	if h == nil {
		return
	}
	if c == nil {
		h.Close()
		return
	}
	h.mu.Lock()
	old := h.client
	h.gen++
	myGen := h.gen
	h.client = c
	listeners := append([]ConnListener(nil), h.listeners...)
	h.mu.Unlock()

	if old != nil && old != c {
		_ = old.Close()
	}
	go h.watch(c, myGen)
	h.notify(listeners, true)
}

// Close 断开并清空共享连接，通知订阅者
func (h *Hub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	c := h.client
	h.client = nil
	h.gen++
	listeners := append([]ConnListener(nil), h.listeners...)
	h.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	h.notify(listeners, false)
}

// Subscribe 注册状态回调，并立即用当前状态调用一次。
func (h *Hub) Subscribe(fn ConnListener) {
	if h == nil || fn == nil {
		return
	}
	h.mu.Lock()
	h.listeners = append(h.listeners, fn)
	connected := h.client != nil
	h.mu.Unlock()
	func() {
		defer func() { _ = recover() }()
		fn(connected)
	}()
}

func (h *Hub) watch(c *Client, gen uint64) {
	_ = c.Wait()
	h.mu.Lock()
	if h.gen != gen || h.client != c {
		h.mu.Unlock()
		return
	}
	h.client = nil
	h.gen++
	listeners := append([]ConnListener(nil), h.listeners...)
	h.mu.Unlock()
	h.notify(listeners, false)
}

func (h *Hub) notify(listeners []ConnListener, connected bool) {
	for _, fn := range listeners {
		if fn == nil {
			continue
		}
		func() {
			defer func() { _ = recover() }()
			fn(connected)
		}()
	}
}
