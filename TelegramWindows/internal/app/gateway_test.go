package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestGatewayVersion(t *testing.T) {
	g := NewGateway("1.2.3", "2026-11-22T15:00:00Z")
	raw := g.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"app.version"}`))
	if len(raw) == 0 {
		t.Fatal("期望有响应，实际为空")
	}
	var resp struct {
		Result map[string]string `json:"result"`
		Error  *RPCError         `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("不应报错: %+v", resp.Error)
	}
	if resp.Result["version"] != "1.2.3" {
		t.Fatalf("版本不符: %v", resp.Result)
	}
}

func TestGatewayMethodNotFound(t *testing.T) {
	g := NewGateway("dev", "now")
	raw := g.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":"a","method":"messages.send"}`))
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("期望 -32601，实际 %+v", resp.Error)
	}
}

func TestGatewayParseError(t *testing.T) {
	g := NewGateway("dev", "now")
	raw := g.Handle(context.Background(), []byte(`{oops`))
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeParseError {
		t.Fatalf("期望 -32700，实际 %+v", resp.Error)
	}
}

func TestGatewayNotificationNoResponse(t *testing.T) {
	g := NewGateway("dev", "now")
	if raw := g.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"app.health"}`)); raw != nil {
		t.Fatalf("通知不应产生响应，实际 %s", raw)
	}
}

func TestGatewayEchoInvalidParams(t *testing.T) {
	g := NewGateway("dev", "now")
	raw := g.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"app.echo","params":"nope"}`))
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("期望 -32602，实际 %+v", resp.Error)
	}
}

func TestGatewayCustomHandlerError(t *testing.T) {
	g := NewGateway("dev", "now")
	g.Register("test.fail", func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New("boom")
	})
	raw := g.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"test.fail"}`))
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeInternalError {
		t.Fatalf("期望 -32603，实际 %+v", resp.Error)
	}
}

func TestBusPublishAndDrop(t *testing.T) {
	bus := NewBus()
	ch, cancel := bus.Subscribe(1)
	defer cancel()

	bus.Publish(Event{Type: "a"})
	select {
	case ev := <-ch:
		if ev.Type != "a" {
			t.Fatalf("事件类型不符: %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到事件")
	}

	// 缓冲区已腾空，再塞满并溢出，验证非阻塞丢弃。
	bus.Publish(Event{Type: "b"})
	bus.Publish(Event{Type: "c"})
	if bus.Dropped() == 0 {
		t.Fatal("拥塞时应有丢弃计数")
	}
}

func TestBusCancelStopsDelivery(t *testing.T) {
	bus := NewBus()
	ch, cancel := bus.Subscribe(4)
	cancel()
	bus.Publish(Event{Type: "x"})
	if _, ok := <-ch; ok {
		t.Fatal("取消后通道应已关闭")
	}
}
