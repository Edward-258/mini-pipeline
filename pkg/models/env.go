package models

import "time"

// Env —— 部署目标环境（T2.1，最小字段集）：
// 一个 env = 集群里的一个 namespace。deploy job（T2.2）拿它决定 patch 哪个 ns 的 Deployment。
// 范围就是一张"名字 → namespace"的映射表：单集群维度，服务清单/状态回收不在面内。
type Env struct {
	Name      string    `bson:"_id"        json:"name"` // env 名即主键
	Namespace string    `bson:"namespace"  json:"namespace"`
	Desc      string    `bson:"desc,omitempty" json:"desc,omitempty"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
}
