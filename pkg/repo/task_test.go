package repo

import (
	"errors"
	"testing"
	"time"

	"pipeline/pkg/config"
	"pipeline/pkg/models"
)

// UpdateTask 的 MatchedCount 守卫（0.7.11）：文档不在 = ErrNotFound，不静默"成功"
// ——静默成功会让致命写调用方（ackStrict/passGate）以为写进去了。
// 直连集群里的 Mongo，属于集成测试（门控同 batch_test.go）：
//
//	kubectl --context k3s-heyuan port-forward -n pipeline svc/mongo 27017:27017
//	MONGO_URI="mongodb://root:<password>@localhost:27017（密码 kubectl get secret mongo-auth -o jsonpath="{.data.password}" | base64 -d 取）" go test ./pkg/repo/ -run TestUpdateTaskGuard -v
func TestUpdateTaskGuard(t *testing.T) {
	if err := Init(config.Load().MongoURI); err != nil {
		t.Skipf("mongo unavailable, skip: %v", err)
	}
	const id = "ackguard-wf-1"
	cleanup := func() {
		if _, err := DeleteTask(id); err != nil && err != ErrNotFound {
			t.Fatalf("cleanup: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	doc := &models.WorkflowTask{ID: id, TaskID: 1, WorkflowName: "ackguard-wf", Status: models.StatusPassed, CreatedAt: time.Now()}
	if err := CreateTask(doc); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := UpdateTask(doc); err != nil { // 在场文档：整树替换正常成功
		t.Fatalf("UpdateTask(existing) = %v, want nil", err)
	}
	if _, err := DeleteTask(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := UpdateTask(doc); !errors.Is(err, ErrNotFound) { // 文档已删：不许静默成功
		t.Fatalf("UpdateTask(deleted) = %v, want ErrNotFound", err)
	}
}
