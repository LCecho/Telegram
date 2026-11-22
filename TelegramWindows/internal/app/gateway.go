// Package app 编排用例，并以 JSON-RPC 2.0 的形式向 UI 暴露能力。
//
// UI（WebView2/测试）只依赖本包的 Gateway 与 EventBus；本包向下依赖 tg/mtproto，
// 绝不反向 import ui。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// JSON-RPC 2.0 错误码。
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Request 是 JSON-RPC 请求（也用于通知：ID 为空）。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 是 JSON-RPC 响应。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError 是对外暴露的错误结构。
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Handler 处理单个方法。
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Gateway 是方法注册表与分发器。
type Gateway struct {
	mu        sync.RWMutex
	handlers  map[string]Handler
	version   string
	buildTime string
}

// NewGateway 创建网关并注册框架内置方法。
func NewGateway(version, buildTime string) *Gateway {
	g := &Gateway{
		handlers:  make(map[string]Handler),
		version:   version,
		buildTime: buildTime,
	}
	g.Register("app.version", func(context.Context, json.RawMessage) (any, error) {
		return map[string]string{"version": version, "buildTime": buildTime}, nil
	})
	g.Register("app.health", func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"ok": true, "methods": g.Methods()}, nil
	})
	g.Register("app.echo", func(_ context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Text string `json:"text"`
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, &RPCError{Code: CodeInvalidParams, Message: "params 必须是 {text:string}"}
			}
		}
		return map[string]string{"text": in.Text}, nil
	})
	return g
}

// Register 注册方法；重复注册会覆盖（便于测试替身）。
func (g *Gateway) Register(method string, h Handler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.handlers[method] = h
}

// Methods 返回已注册方法名（排序后）。
func (g *Gateway) Methods() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.handlers))
	for m := range g.handlers {
		out = append(out, m)
	}
	// 方法数量很少，插入排序足够且避免额外 import。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Handle 处理一条原始 JSON-RPC 报文，返回序列化后的响应。
//
// 通知（无 id）成功时返回 nil，调用方据此决定是否回写。
func (g *Gateway) Handle(ctx context.Context, raw []byte) []byte {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return mustMarshal(Response{
			JSONRPC: "2.0",
			Error:   &RPCError{Code: CodeParseError, Message: "无法解析请求 JSON"},
		})
	}
	resp := g.dispatch(ctx, &req)
	if resp == nil {
		return nil
	}
	return mustMarshal(resp)
}

func (g *Gateway) dispatch(ctx context.Context, req *Request) *Response {
	if req.Method == "" {
		return &Response{JSONRPC: "2.0", ID: req.ID,
			Error: &RPCError{Code: CodeInvalidRequest, Message: "缺少 method"}}
	}

	g.mu.RLock()
	h, ok := g.handlers[req.Method]
	g.mu.RUnlock()
	if !ok {
		return &Response{JSONRPC: "2.0", ID: req.ID,
			Error: &RPCError{Code: CodeMethodNotFound, Message: "未知方法：" + req.Method}}
	}

	result, err := h(ctx, req.Params)
	if err != nil {
		return &Response{JSONRPC: "2.0", ID: req.ID, Error: toRPCError(err)}
	}
	if len(req.ID) == 0 {
		return nil // 通知不需要响应
	}
	return &Response{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func toRPCError(err error) *RPCError {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return &RPCError{Code: CodeInternalError, Message: err.Error()}
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"响应序列化失败"}}`)
	}
	return b
}

// Event 是推送给 UI 的事件。
type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// Bus 是进程内事件总线。
//
// 投递为非阻塞：订阅者处理不过来时丢弃并计数，避免拖垮 UpdatePump。
type Bus struct {
	mu      sync.RWMutex
	subs    []chan Event
	dropped atomic.Int64
}

// NewBus 创建事件总线。
func NewBus() *Bus { return &Bus{} }

// Subscribe 注册订阅者，返回只读通道与取消函数。
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	ch := make(chan Event, buffer)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			for i, s := range b.subs {
				if s == ch {
					b.subs = append(b.subs[:i], b.subs[i+1:]...)
					break
				}
			}
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, cancel
}

// Publish 非阻塞广播事件。
func (b *Bus) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			b.dropped.Add(1)
		}
	}
}

// Dropped 返回累计丢弃事件数（监控用）。
func (b *Bus) Dropped() int64 { return b.dropped.Load() }
