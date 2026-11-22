package session

import (
	"testing"
	"time"
)

func TestNextMessageIDAlignedAndMonotonic(t *testing.T) {
	base := time.Date(2026, 11, 22, 15, 30, 0, 0, time.UTC).UnixMilli()

	var last int64
	for i := 0; i < 500; i++ {
		id := NextMessageID(base+int64(i), 0, last)
		if id%4 != 0 {
			t.Fatalf("第 %d 次：msg_id %d 未 4 字节对齐", i, id)
		}
		if id <= last {
			t.Fatalf("第 %d 次：msg_id 未递增（last=%d id=%d）", i, last, id)
		}
		last = id
	}
}

// 时钟回拨：仍必须严格递增。
func TestNextMessageIDClockBackwards(t *testing.T) {
	future := int64(1_800_000_000_000)
	id1 := NextMessageID(future, 0, 0)
	id2 := NextMessageID(future-60_000, 0, id1)
	if id2 <= id1 {
		t.Fatalf("时钟回拨后未保持递增：id1=%d id2=%d", id1, id2)
	}
	if id2%4 != 0 {
		t.Fatalf("回拨分支未对齐：%d", id2)
	}
}

// 同一毫秒重复调用也不能重复。
func TestNextMessageIDSameMillisecond(t *testing.T) {
	now := int64(1_800_000_000_123)
	id1 := NextMessageID(now, 0, 0)
	id2 := NextMessageID(now, 0, id1)
	if id2 <= id1 {
		t.Fatalf("同毫秒重复调用未递增：%d / %d", id1, id2)
	}
}

// 高位应等于秒数（允许 1 秒误差，避免对齐带来的偏移误判）。
func TestNextMessageIDEncodesUnixSeconds(t *testing.T) {
	const ms = int64(1_800_000_000_500)
	id := NextMessageID(ms, 0, 0)
	gotSec := id >> 32
	wantSec := ms / 1000
	if diff := gotSec - wantSec; diff < 0 || diff > 1 {
		t.Fatalf("秒数编码偏差过大：got=%d want=%d", gotSec, wantSec)
	}
}

// 时间差校正应把 msg_id 推前对应秒数。
func TestNextMessageIDTimeDiff(t *testing.T) {
	const ms = int64(1_800_000_000_000)
	a := NextMessageID(ms, 0, 0)
	b := NextMessageID(ms, 3600, 0)
	if b-a < 3600<<32 || b-a > (3600<<32)+4 {
		t.Fatalf("时间差校正不正确：delta=%d", b-a)
	}
}

func TestGeneratorConcurrent(t *testing.T) {
	g := NewGenerator(0)
	now := time.Now()

	const workers, perWorker = 8, 200
	results := make(chan int64, workers*perWorker)
	done := make(chan struct{})

	for w := 0; w < workers; w++ {
		go func() {
			for i := 0; i < perWorker; i++ {
				results <- g.Next(now)
			}
			done <- struct{}{}
		}()
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	close(results)

	seen := make(map[int64]struct{}, workers*perWorker)
	for id := range results {
		if _, dup := seen[id]; dup {
			t.Fatalf("出现重复 msg_id：%d", id)
		}
		if id%4 != 0 {
			t.Fatalf("并发生成未对齐：%d", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("数量不符：got=%d want=%d", len(seen), workers*perWorker)
	}
}
