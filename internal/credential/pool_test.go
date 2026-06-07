package credential

import (
	"encoding/json"
	"testing"
	"time"
)

// helper: 创建 N 个凭证
func makeCreds(n int) []Credential {
	creds := make([]Credential, n)
	for i := range creds {
		creds[i] = Credential{
			Provider:    "test",
			ID:          string(rune('a' + i)),
			Label:       string(rune('A' + i)),
			AuthType:    "api_key",
			Priority:    i,
			AccessToken: "tok-" + string(rune('a'+i)),
		}
	}
	return creds
}

// ───────────────────────────── NewPool ─────────────────────────────

func TestNewPool(t *testing.T) {
	p := NewPool()
	if p.Count() != 0 {
		t.Fatalf("新池应为空, 实际 %d", p.Count())
	}
	if p.strategy != "fill_first" {
		t.Fatalf("默认策略应为 fill_first, 实际 %s", p.strategy)
	}
	if p.exhaustCooldown != 30*time.Minute {
		t.Fatalf("默认冷却时间应为 30m, 实际 %v", p.exhaustCooldown)
	}
}

// ───────────────────────────── cryptoRandIntn ─────────────────────────────

func TestCryptoRandIntn(t *testing.T) {
	// n <= 0 返回 0
	if v := cryptoRandIntn(0); v != 0 {
		t.Fatalf("cryptoRandIntn(0) = %d, 期望 0", v)
	}
	if v := cryptoRandIntn(-1); v != 0 {
		t.Fatalf("cryptoRandIntn(-1) = %d, 期望 0", v)
	}
	// 正常范围 [0, n)
	for i := 0; i < 100; i++ {
		v := cryptoRandIntn(10)
		if v < 0 || v >= 10 {
			t.Fatalf("cryptoRandIntn(10) = %d, 超出 [0,10)", v)
		}
	}
}

// ───────────────────────────── SetStrategy / SetExhaustCooldown / ApplyConfig ─────────────────────────────

func TestSetStrategy(t *testing.T) {
	p := NewPool()
	for _, s := range []string{"round_robin", "random", "least_used"} {
		p.SetStrategy(s)
		if p.strategy != s {
			t.Fatalf("策略应为 %s, 实际 %s", s, p.strategy)
		}
	}
}

func TestSetExhaustCooldown(t *testing.T) {
	p := NewPool()
	d := 5 * time.Minute
	p.SetExhaustCooldown(d)
	if p.exhaustCooldown != d {
		t.Fatalf("冷却时间应为 %v, 实际 %v", d, p.exhaustCooldown)
	}
}

func TestApplyConfig(t *testing.T) {
	p := NewPool()
	p.ApplyConfig(CredentialConfig{Selection: "round_robin"})
	if p.strategy != "round_robin" {
		t.Fatalf("ApplyConfig 应设置策略为 round_robin, 实际 %s", p.strategy)
	}
	// 空配置不应改变策略
	p.ApplyConfig(CredentialConfig{})
	if p.strategy != "round_robin" {
		t.Fatalf("空配置不应改变策略")
	}
}

// ───────────────────────────── Add / Count / Credentials / UseCounts ─────────────────────────────

func TestAddAndCount(t *testing.T) {
	p := NewPool()
	creds := makeCreds(3)
	for _, c := range creds {
		p.Add(c)
	}
	if p.Count() != 3 {
		t.Fatalf("Count = %d, 期望 3", p.Count())
	}
}

func TestCredentialsSnapshot(t *testing.T) {
	p := NewPool()
	creds := makeCreds(2)
	for _, c := range creds {
		p.Add(c)
	}
	snap := p.Credentials()
	if len(snap) != 2 {
		t.Fatalf("快照长度 = %d, 期望 2", len(snap))
	}
	// 修改快照不应影响原始
	snap[0].ID = "modified"
	orig := p.Credentials()
	if orig[0].ID == "modified" {
		t.Fatal("修改快照不应影响原始凭证")
	}
}

func TestUseCountsSnapshot(t *testing.T) {
	p := NewPool()
	for _, c := range makeCreds(2) {
		p.Add(c)
	}
	counts := p.UseCounts()
	if len(counts) != 2 {
		t.Fatalf("UseCounts 长度 = %d, 期望 2", len(counts))
	}
	counts[0] = 999
	orig := p.UseCounts()
	if orig[0] == 999 {
		t.Fatal("修改快照不应影响原始计数")
	}
}

// ───────────────────────────── Select - fill_first ─────────────────────────────

func TestSelectFillFirst(t *testing.T) {
	p := NewPool()
	creds := makeCreds(3)
	for _, c := range creds {
		p.Add(c)
	}
	// fill_first 始终返回第一个
	for i := 0; i < 5; i++ {
		c, ok := p.Select()
		if !ok {
			t.Fatal("Select 应成功")
		}
		if c.ID != "a" {
			t.Fatalf("fill_first 应返回第一个凭证, 实际 %s", c.ID)
		}
	}
	// 使用计数: [5, 0, 0]
	counts := p.UseCounts()
	if counts[0] != 5 {
		t.Fatalf("第一个凭证使用次数 = %d, 期望 5", counts[0])
	}
}

func TestSelectEmptyPool(t *testing.T) {
	p := NewPool()
	_, ok := p.Select()
	if ok {
		t.Fatal("空池 Select 应返回 false")
	}
}

// ───────────────────────────── Select - round_robin ─────────────────────────────

func TestSelectRoundRobin(t *testing.T) {
	p := NewPool()
	p.SetStrategy("round_robin")
	creds := makeCreds(3)
	for _, c := range creds {
		p.Add(c)
	}
	// 连续选择应轮询
	expected := []string{"a", "b", "c", "a", "b", "c"}
	for i, exp := range expected {
		c, ok := p.Select()
		if !ok {
			t.Fatalf("第 %d 次 Select 失败", i)
		}
		if c.ID != exp {
			t.Fatalf("第 %d 次: 期望 %s, 实际 %s", i, exp, c.ID)
		}
	}
}

// ───────────────────────────── Select - random ─────────────────────────────

func TestSelectRandom(t *testing.T) {
	p := NewPool()
	p.SetStrategy("random")
	creds := makeCreds(5)
	for _, c := range creds {
		p.Add(c)
	}
	// 随机策略: 多次选择应命中不同凭证 (概率性, 但 100 次中至少命中 3 个)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		c, ok := p.Select()
		if !ok {
			t.Fatal("Select 应成功")
		}
		seen[c.ID] = true
	}
	if len(seen) < 3 {
		t.Fatalf("随机策略 100 次应至少命中 3 个不同凭证, 实际 %d", len(seen))
	}
}

// ───────────────────────────── Select - least_used ─────────────────────────────

func TestSelectLeastUsed(t *testing.T) {
	p := NewPool()
	p.SetStrategy("least_used")
	creds := makeCreds(3)
	for _, c := range creds {
		p.Add(c)
	}
	// least_used 应均匀分配
	for i := 0; i < 9; i++ {
		_, ok := p.Select()
		if !ok {
			t.Fatalf("第 %d 次 Select 失败", i)
		}
	}
	counts := p.UseCounts()
	// 3 个凭证各 3 次
	for i, c := range counts {
		if c != 3 {
			t.Fatalf("凭证 %d 使用次数 = %d, 期望 3", i, c)
		}
	}
}

// ───────────────────────────── MarshalJSON ─────────────────────────────

func TestMarshalJSON(t *testing.T) {
	p := NewPool()
	p.SetStrategy("round_robin")
	p.Add(Credential{Provider: "openai", ID: "key1", Label: "Main Key", AuthType: "api_key"})

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("MarshalJSON 失败: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("解析 JSON 失败: %v", err)
	}

	if result["strategy"] != "round_robin" {
		t.Fatalf("strategy = %v, 期望 round_robin", result["strategy"])
	}

	creds, ok := result["credentials"].([]any)
	if !ok || len(creds) != 1 {
		t.Fatalf("credentials 长度 = %d, 期望 1", len(creds))
	}

	c0 := creds[0].(map[string]any)
	if c0["id"] != "key1" {
		t.Fatalf("id = %v, 期望 key1", c0["id"])
	}
	if c0["provider"] != "openai" {
		t.Fatalf("provider = %v, 期望 openai", c0["provider"])
	}
}

func TestMarshalJSON_EmptyPool(t *testing.T) {
	p := NewPool()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("空池 MarshalJSON 失败: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	if result["strategy"] != "fill_first" {
		t.Fatalf("空池 strategy = %v", result["strategy"])
	}
	creds, _ := result["credentials"].([]any)
	if len(creds) != 0 {
		t.Fatalf("空池 credentials 长度 = %d, 期望 0", len(creds))
	}
}

// ───────────────────────────── Credential 字段完整性 ─────────────────────────────

func TestSelectCredentialFields(t *testing.T) {
	p := NewPool()
	original := Credential{
		Provider:    "anthropic",
		ID:          "sk-test-123",
		Label:       "Production Key",
		AuthType:    "api_key",
		Priority:    1,
		Source:      "env:ANTHROPIC_API_KEY",
		AccessToken: "sk-ant-xxx",
		BaseURL:     "https://api.anthropic.com",
	}
	p.Add(original)

	c, ok := p.Select()
	if !ok {
		t.Fatal("Select 应成功")
	}
	if c.Provider != original.Provider {
		t.Fatalf("Provider = %s, 期望 %s", c.Provider, original.Provider)
	}
	if c.AccessToken != original.AccessToken {
		t.Fatalf("AccessToken = %s, 期望 %s", c.AccessToken, original.AccessToken)
	}
	if c.BaseURL != original.BaseURL {
		t.Fatalf("BaseURL = %s, 期望 %s", c.BaseURL, original.BaseURL)
	}
	if c.Source != original.Source {
		t.Fatalf("Source = %s, 期望 %s", c.Source, original.Source)
	}
}
