package platform

import "time"

// Clock 抽象时间来源，便于测试注入固定时间。
type Clock interface {
	Now() time.Time
}

// SystemClock 使用系统时间。
type SystemClock struct{}

// Now 返回当前时间。
func (SystemClock) Now() time.Time { return time.Now() }

// FixedClock 用于测试。
type FixedClock struct {
	T time.Time
}

// Now 返回固定时间。
func (c *FixedClock) Now() time.Time { return c.T }

// Advance 推进固定时间。
func (c *FixedClock) Advance(d time.Duration) { c.T = c.T.Add(d) }
