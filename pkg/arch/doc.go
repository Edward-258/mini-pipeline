// Package arch 承载分层方向纪律的断言测试（TestLayering，见 arch_test.go）。
//
// 本包没有任何生产代码：纪律断言全部走 go 工具链的包图查询（go list），
// 不 import 任何被检包，因此独立成子包即可让断言对全仓成立；反过来，
// 任何被检包若 import 本包，也会被规则当场抓住，不存在自证清白的死角。
//
// 依赖方向铁律（单向向下、无环）：真实包图是分层图不是单链——同层并列的包
// 互不依赖，每行右侧即该行各包的全部模块内直接依赖（go list 实测快照；
// 包图会随演进漂移，活的纪律以 arch_test.go 的断言为准）：
//
//	config  models                             （零模块内依赖的叶子）
//	repo  step  webhook  engine/cancel         （← models）
//	k8s                                        （← config + models）
//	engine/notify  engine/cron                 （← models + repo）
//	engine/exec                                （← config + models + k8s + step + engine/cancel）
//	engine 编排本体                            （← config + models + repo + k8s + cancel/exec/notify）
//	api                                        （← engine + webhook + repo + models）
//	main                                       （← api + config + engine + engine/cron + k8s + repo）
//
// 引擎子包与编排本体分行列出是有意的：exec 依赖兄弟子包 cancel 属正常形态，
// 规则 2 禁的是 exec 的依赖闭包出现编排本体，不禁兄弟子包。
//
// 四条会咬人的规则（命中即红灯，口径细节见 arch_test.go）：
//  1. models/repo/step 的依赖闭包不得出现 engine 层与 api 层的包
//  2. engine/exec 的依赖闭包不得出现 engine 编排本体与 api 层（exec 只被引擎驱动）
//  3. api 的 import（含测试文件）不得直接出现 k8s 与 step——K8s 访问经钩子注入，api 只做 HTTP 形状翻译
//  4. step 的依赖闭包不得出现 repo——脚本执行层只准依赖 models
package arch
