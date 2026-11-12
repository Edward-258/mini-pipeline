package exec

import (
	"strings"
	"testing"

	"pipeline/pkg/models"
)

// TestResolveHosts —— 名字簿解析表测（Ctx stub，不摸持久层）：
// 顺序保持、重复引用去重、缺失名字报错点名。
func TestResolveHosts(t *testing.T) {
	c := &Ctx{FindHost: func(name string) (*models.Host, error) {
		if name == "ghost" {
			return nil, repoErrNotFound
		}
		return &models.Host{Name: name, UserName: "root", IP: "10.0.0.1", Port: 22, PrivateKey: "k"}, nil
	}}

	out, err := resolveHosts(c, []string{"a", "b", "a"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(out) != 2 || out[0].Name != "a" || out[1].Name != "b" {
		t.Fatalf("dedupe/order broken: %+v", out)
	}

	// 缺失点名：错误信息必须带主机名（用户照着去建记录）
	_, err = resolveHosts(c, []string{"ghost"})
	if err == nil || !strings.Contains(err.Error(), `"ghost"`) {
		t.Fatalf("missing host must be named in the error, got %v", err)
	}
}
