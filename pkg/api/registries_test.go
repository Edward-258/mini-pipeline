package api

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"pipeline/pkg/models"
)

// TestRegistriesCRUD —— 仓库管理全链路回环（真 mongo + httptest，putTestServer 同纪律）：
// CRUD 全路径 + is_default 全量制语义（建立翻转旧默认 / 更新改靶 / 删除默认不选举）
// + 同步钩子触发断言（决断 #16：CRUD 成功即同步白名单，失败路径不触发）。
func TestRegistriesCRUD(t *testing.T) {
	// 同步计数（决断 #16）：create A / create B / delete B = 恰 3 次成功触发；
	// 400/409/404 失败路径零触发。stub 经 Deps 随路由构造注入，不碰任何全局
	// （快照恢复 defer 里的 PUT 也会触发，无害——发生在断言之后）。
	syncCalls := 0
	base := putTestServer(t, Deps{BuildkitSync: func() { syncCalls++ }})
	pfx := fmt.Sprintf("reg-%d", time.Now().UnixNano()%100000)
	nameA, nameB := pfx+"-a", pfx+"-b"
	defer httpJSON(t, "DELETE", base+"/api/registries/"+nameA, nil)
	defer httpJSON(t, "DELETE", base+"/api/registries/"+nameB, nil)
	// 共享库隔离：库里有生产记录（如 acr/local-ingress），测试建默认记录会翻转它们的
	// 默认位——先快照，defer 按快照恢复（bootstrap-5 gate 活体教训：不能假设库里只有自己的数据）
	snapshot := map[string]bool{}
	code0, body0 := httpJSON(t, "GET", base+"/api/registries", nil)
	if code0 == 200 {
		var pre []*models.Registry
		json.Unmarshal(body0, &pre)
		for _, r := range pre {
			snapshot[r.Name] = r.IsDefault
		}
	}
	defer func() {
		for name, wasDefault := range snapshot {
			if name == nameA || name == nameB {
				continue
			}
			cur := listOne(t, base, name)
			if cur != nil && cur.IsDefault != wasDefault {
				cur.IsDefault = wasDefault
				httpJSON(t, "PUT", base+"/api/registries/"+name, cur)
			}
		}
	}()

	// 400：address 必填
	if code, _ := httpJSON(t, "POST", base+"/api/registries", &models.Registry{Name: nameA}); code != 400 {
		t.Fatalf("empty address: want 400, got %d", code)
	}
	// scheme 声明保留（决断 #16 翻转了旧的"入库剥 scheme"）：https:// 前缀原样入库，
	// 消费点（烘焙/白名单键）经 NormalizeRegistryAddr 现剥——数据不丢语义
	code, body := httpJSON(t, "POST", base+"/api/registries", &models.Registry{
		Name: nameA, Address: "https://reg-a.example.com", Username: "u", Password: "p", IsDefault: true,
	})
	if code != 200 {
		t.Fatalf("create A: want 200, got %d: %s", code, body)
	}
	var gotA models.Registry
	json.Unmarshal(body, &gotA)
	if gotA.Address != "https://reg-a.example.com" {
		t.Fatalf("scheme declaration must be preserved: %q", gotA.Address)
	}
	if !gotA.IsDefault {
		t.Fatalf("A should be default (first registry)")
	}
	if syncCalls != 1 {
		t.Fatalf("hook must fire once after create A, got %d", syncCalls)
	}

	// B 标默认 → A 的默认位被翻转（全局唯一）
	if code, _ = httpJSON(t, "POST", base+"/api/registries", &models.Registry{
		Name: nameB, Address: "reg-b.example.com", IsDefault: true,
	}); code != 200 {
		t.Fatalf("create B: want 200, got %d", code)
	}
	defA, defB := mustReg(t, base, nameA), mustReg(t, base, nameB)
	if defA.IsDefault || !defB.IsDefault {
		t.Fatalf("default flip failed: A=%v B=%v", defA.IsDefault, defB.IsDefault)
	}

	// 409：重名
	if code, _ = httpJSON(t, "POST", base+"/api/registries", &models.Registry{Name: nameA, Address: "x"}); code != 409 {
		t.Fatalf("duplicate create: want 409, got %d", code)
	}
	// 400：body/path 名字不一致
	if code, _ = httpJSON(t, "PUT", base+"/api/registries/"+nameA, &models.Registry{Name: "someone-else", Address: "x"}); code != 400 {
		t.Fatalf("name mismatch: want 400, got %d", code)
	}

	// 删除默认 B → 回落"无默认"稳态（不做选举补位：强行选举会让裸名
	// 目标静默漂到任意历史记录——bootstrap-5 gate 在共享库里活体抓的；决断 #16 起无默认
	// = 裸名解析报错点名，同样不该静默换靶）
	if code, _ = httpJSON(t, "DELETE", base+"/api/registries/"+nameB, nil); code != 200 {
		t.Fatalf("delete B: want 200, got %d", code)
	}
	if got := mustReg(t, base, nameA); got.IsDefault {
		t.Fatalf("no election expected after deleting default B (fall back to no-default steady state)")
	}
	// 404：删不存在的
	if code, _ = httpJSON(t, "DELETE", base+"/api/registries/"+nameB, nil); code != 404 {
		t.Fatalf("delete missing: want 404, got %d", code)
	}
	// 钩子触发总账：成功路径 create A / create B / delete B 恰 3 次，失败路径（400/409/404）零次
	if syncCalls != 3 {
		t.Fatalf("BuildkitSync must fire exactly 3 times (createA+createB+deleteB), got %d", syncCalls)
	}
}

// listOne —— 按名取单条（不存在返回 nil；隔离恢复逻辑用）
func listOne(t *testing.T, base, name string) *models.Registry {
	t.Helper()
	code, body := httpJSON(t, "GET", base+"/api/registries", nil)
	if code != 200 {
		return nil
	}
	var list []*models.Registry
	json.Unmarshal(body, &list)
	for _, r := range list {
		if r.Name == name {
			return r
		}
	}
	return nil
}

func mustReg(t *testing.T, base, name string) *models.Registry {
	t.Helper()
	code, body := httpJSON(t, "GET", base+"/api/registries", nil)
	if code != 200 {
		t.Fatalf("list registries: want 200, got %d", code)
	}
	var list []*models.Registry
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, r := range list {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("registry %s not in list", name)
	return nil
}
