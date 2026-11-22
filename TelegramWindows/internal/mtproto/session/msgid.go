// Package session 维护 MTProto 会话级状态（msg_id / seq_no / salt）。
//
// 当前仅实现 msg_id 生成，规则见 TelegramWindows/docs/API-ANALYSIS.md §6：
// 参考实现 ConnectionsManager.cpp:603。
package session

import (
	"sync"
	"time"
)

// messageIDShift 是 msg_id 的时间换算基：1 秒 = 2^32。
const messageIDShift = 32

// NextMessageID 计算下一个 msg_id。
//
//	msg_id = (serverTimeMs / 1000) << 32 | (serverTimeMs % 1000) * 2^32 / 1000
//
// 约束（与官方实现一致）：
//  1. 严格大于 last（时钟回拨时靠 last+1 兜底）；
//  2. 必须 4 字节对齐（while id%4 != 0 { id++ }）。
//
// 用整数拆解代替浮点，避免 float64 精度导致的跳变。
func NextMessageID(nowMs int64, diffSec int32, last int64) int64 {
	ms := nowMs + int64(diffSec)*1000
	sec := ms / 1000
	frac := (ms % 1000) << messageIDShift / 1000
	id := sec<<messageIDShift + frac

	if id <= last {
		id = last + 1
	}
	if rem := id % 4; rem != 0 {
		id += 4 - rem
	}
	return id
}

// Generator 是并发安全的 msg_id 生成器（每个会话/连接一个实例）。
type Generator struct {
	mu      sync.Mutex
	last    int64
	diffSec int32
}

// NewGenerator 创建生成器；difSec 为服务端时间差（秒），未校准时传 0。
func NewGenerator(diffSec int32) *Generator {
	return &Generator{diffSec: diffSec}
}

// SetTimeDiff 更新时间差校正值（收到时间同步结果后调用）。
func (g *Generator) SetTimeDiff(seconds int32) {
	g.mu.Lock()
	g.diffSec = seconds
	g.mu.Unlock()
}

// Last 返回最近一次生成的 msg_id（测试与调试用）。
func (g *Generator) Last() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last
}

// Next 生成下一个 msg_id。
func (g *Generator) Next(now time.Time) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.last = NextMessageID(now.UnixMilli(), g.diffSec, g.last)
	return g.last
}
