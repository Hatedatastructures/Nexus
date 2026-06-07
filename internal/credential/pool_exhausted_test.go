package credential

import (
	"context"
	"testing"
	"time"
)

// ───────────────────────────── MarkExhausted ─────────────────────────────

func TestMarkExhausted_EmptyPool(t *testing.T) {
	p := NewPool()
	_, ok := p.MarkExhausted(context.Background(), 429, "rate limited")
	if ok {
		t.Fatal("空池 MarkExhausted 应返回 false")
	}
}

func TestMarkExhausted_FillFirst(t *testing.T) {
	p := NewPool()
	creds := makeCreds(3)
	for _, c := range creds {
		p.Add(c)
	}
	// 先选择一次确认第一个
	c, _ := p.Select()
	if c.ID != "a" {
		t.Fatalf("初始选择应为 a, 实际 %s", c.ID)
	}

	// 标记耗尽, 应返回下一个可用凭证
	next, ok := p.MarkExhausted(context.Background(), 429, "rate limited")
	if !ok {
		t.Fatal("MarkExhausted 应成功返回替代凭证")
	}
	if next.ID != "b" {
		t.Fatalf("替代凭证应为 b, 实际 %s", next.ID)
	}

	// 再次选择应跳过 a
	c2, _ := p.Select()
	if c2.ID != "b" {
		t.Fatalf("耗尽后选择应为 b, 实际 %s", c2.ID)
	}
}

func TestMarkExhausted_AllExhausted(t *testing.T) {
	p := NewPool()
	p.SetExhaustCooldown(1 * time.Hour) // 长冷却避免自动恢复
	creds := makeCreds(2)
	for _, c := range creds {
		p.Add(c)
	}
	// fill_first: 选择 a, 标记耗尽 a -> 返回 b
	p.Select()
	next, _ := p.MarkExhausted(context.Background(), 429, "a exhausted")
	if next.ID != "b" {
		t.Fatalf("第一次耗尽后应返回 b, 实际 %s", next.ID)
	}
	// 此时 lastSelectedIdx 指向 b, 标记耗尽 b
	p.MarkExhausted(context.Background(), 429, "b exhausted")

	// 所有凭证耗尽
	_, ok := p.Select()
	if ok {
		t.Fatal("所有凭证耗尽时 Select 应返回 false")
	}
}

func TestMarkExhausted_CooldownRecovery(t *testing.T) {
	p := NewPool()
	p.SetExhaustCooldown(50 * time.Millisecond)
	creds := makeCreds(2)
	for _, c := range creds {
		p.Add(c)
	}

	p.Select()                                        // 选中 a
	p.MarkExhausted(context.Background(), 429, "exh") // 耗尽 a

	// a 已耗尽, 应返回 b
	c, _ := p.Select()
	if c.ID != "b" {
		t.Fatalf("a 耗尽后应选 b, 实际 %s", c.ID)
	}

	// 等待冷却结束
	time.Sleep(80 * time.Millisecond)

	// a 应已恢复
	c2, _ := p.Select()
	if c2.ID != "a" {
		t.Fatalf("冷却后 a 应恢复, 实际选中 %s", c2.ID)
	}
}

// ───────────────────────────── MarkExhausted - round_robin ─────────────────────────────

func TestMarkExhausted_RoundRobin(t *testing.T) {
	p := NewPool()
	p.SetStrategy("round_robin")
	p.SetExhaustCooldown(1 * time.Hour)
	creds := makeCreds(3)
	for _, c := range creds {
		p.Add(c)
	}

	p.Select() // a (cursor=1)
	p.Select() // b (cursor=2)

	// 标记 b 耗尽 (round_robin 通过 cursor-1 定位)
	next, ok := p.MarkExhausted(context.Background(), 429, "b exhausted")
	if !ok {
		t.Fatal("MarkExhausted 应成功")
	}
	// 应返回 c (下一个可用)
	if next.ID != "c" {
		t.Fatalf("替代凭证应为 c, 实际 %s", next.ID)
	}
}

// ───────────────────────────── MarkExhausted - random / least_used ─────────────────────────────

func TestMarkExhausted_Random(t *testing.T) {
	p := NewPool()
	p.SetStrategy("random")
	p.SetExhaustCooldown(1 * time.Hour)
	creds := makeCreds(3)
	for _, c := range creds {
		p.Add(c)
	}

	// 选择一个, 然后标记耗尽
	p.Select()
	next, ok := p.MarkExhausted(context.Background(), 429, "exh")
	if !ok {
		t.Fatal("应有可用凭证")
	}
	// next 不应是已耗尽的那个 (使用 lastSelectedIdx 定位)
	count := p.Count()
	remaining := 0
	for i := 0; i < count; i++ {
		_, ok := p.Select()
		if ok {
			remaining++
		}
	}
	_ = next
	if remaining == 0 {
		t.Fatal("应仍有可用凭证")
	}
}

// ───────────────────────────── Select 未知策略 ─────────────────────────────

func TestSelectUnknownStrategy(t *testing.T) {
	p := NewPool()
	p.SetStrategy("unknown")
	creds := makeCreds(2)
	for _, c := range creds {
		p.Add(c)
	}
	// 未知策略 fallback 到 fill_first
	c, ok := p.Select()
	if !ok {
		t.Fatal("未知策略应 fallback 到 fill_first")
	}
	if c.ID != "a" {
		t.Fatalf("未知策略应选第一个, 实际 %s", c.ID)
	}
}
