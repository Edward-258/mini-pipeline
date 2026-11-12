import {
  Button, Col, Drawer, Dropdown, Input, InputNumber, Popconfirm, Radio, Row, Select, Space, Switch, Tag, Typography,
} from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import StepForm from './StepForm'
import {
  addStep, moveStep, patchDeploy, patchFreestyle, patchJob, patchStep, removeStep,
} from './tree'
import type { Env, ErrorPolicy, Host, Job, JobType, Registry, StepType, Workflow } from '../../types'

const { Text } = Typography

// 步骤类型四选一：git 不在列——克隆已收口到最左代码源（决断 #18，clone 唯一化），
// git 只是引擎内部合成 checkout / 存量档案重放的执行实现，不再是用户可手的步骤；
// JSON 模式逃生舱仍接受 git（多仓边角），旧档案的 git 步骤照常渲染编辑。
const STEP_ADD_OPTIONS: { value: StepType; label: string }[] = [
  { value: 'shell', label: '+ shell' },
  { value: 'docker_build', label: '+ 镜像构建' },
  { value: 'junit_report', label: '+ 测试报告' },
  { value: 'archive', label: '+ 归档' },
]

interface Props {
  wf: Workflow
  si: number
  ji: number
  envs: Env[] // 编辑器页拉一次下传（旧实现在每个 deploy JobCard 各拉）
  registries: Registry[]
  hosts: Host[]
  // 更新式契约：收 (prev) => next 的 updater 而非下一帧值——handler 只捕获 (si,ji,pi) 稳定坐标，
  // 不捕获每键变化的 wf；配合 React Compiler，打字时兄弟步骤整体跳过重渲
  onChange: (update: (prev: Workflow) => Workflow) => void
  onClose: () => void
  onRemove: () => void
}

// job 详情抽屉——画布条框的完整编辑面。
// live-patch：改动即时进 wf，关抽屉即保留（无确定/取消缓冲，误改走 JSON 模式兜底）。
export default function JobDrawer({ wf, si, ji, envs, registries, hosts, onChange, onClose, onRemove }: Props) {
  const job: Job = wf.stages[si].jobs[ji]
  const spec = job.spec ?? { steps: [] }
  const steps = spec.steps ?? []
  // 命名空间建议：同 job 其余 docker_build 步骤 image 首个 / 前段去重（StepForm 镜像名拆分后的 AutoComplete 数据源）
  const nsOptions = [...new Set(steps.flatMap((s) => {
    const img = s.image ?? ''
    return s.type === 'docker_build' && img.includes('/') ? [img.slice(0, img.indexOf('/'))] : []
  }))]

  return (
    <Drawer
      open
      width={560}
      onClose={onClose}
      title={
        <Space size={10}>
          <Text strong>编辑任务</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>stage: {wf.stages[si].name}</Text>
        </Space>
      }
      extra={
        <Popconfirm title={`删除 job ${job.name}？`} onConfirm={onRemove}>
          <Button danger icon={<DeleteOutlined />}>删除</Button>
        </Popconfirm>
      }
    >
      <Space direction="vertical" size={14} style={{ width: '100%' }}>
        <div>
          <Text type="secondary" style={{ fontSize: 12 }}>任务名</Text>
          <Input
            value={job.name} placeholder="job 名"
            onChange={(e) => onChange((prev) => patchJob(prev, si, ji, { name: e.target.value }))}
          />
        </div>

        <div>
          <Text type="secondary" style={{ fontSize: 12 }}>类型</Text>{' '}
          <Radio.Group
            value={job.type ?? 'freestyle'}
            onChange={(e) => {
              const t = e.target.value as JobType
              // 切换类型时重置对应 spec（旧 spec 残留会让后端校验迷惑）
              onChange((prev) => patchJob(prev, si, ji,
                t === 'freestyle'
                  ? { type: t, spec: { steps: [] }, deploy_spec: undefined }
                  : { type: t, spec: undefined, deploy_spec: { env: '', deployment_name: '', container_name: '', image: '' } }))
            }}
          >
            <Radio.Button value="freestyle">freestyle</Radio.Button>
            <Radio.Button value="deploy">deploy</Radio.Button>
          </Radio.Group>
          {job.type === 'deploy'
            ? <Tag color="purple">K8s 直 patch</Tag>
            : <Tag color="cyan">K8s Job</Tag>}
        </div>

        {(job.type ?? 'freestyle') === 'freestyle' ? (
          <>
            {/* 执行镜像：前端只走自动档（引擎按步骤选 runner/busybox，pkg/k8s/build.go 镜像三档）——
                自定义镜像选择已删（用户拍板：纯累赘）；旧图纸残留的显式镜像给清除入口 */}
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>执行镜像</Text>{' '}
              {spec.image ? (
                <Space size={6} wrap>
                  <Tag color="orange">{spec.image}</Tag>
                  <Text type="secondary" style={{ fontSize: 12 }}>显式指定（压过自动档）</Text>
                  <Button size="small" onClick={() => onChange((prev) => patchFreestyle(prev, si, ji, { image: '' }))}>
                    清除，走自动档
                  </Button>
                </Space>
              ) : (
                <Text style={{ fontSize: 12 }}>自动档 —— git/构建/归档 → runner，纯 shell → busybox</Text>
              )}
            </div>
            <Row gutter={10}>
              <Col span={6}>
                <Text type="secondary" style={{ fontSize: 12 }}>重试</Text>
                <InputNumber style={{ width: '100%' }} min={0} max={5} value={spec.retry ?? 0}
                  onChange={(v) => onChange((prev) => patchFreestyle(prev, si, ji, { retry: v ?? 0 }))} />
              </Col>
              <Col span={6}>
                <Text type="secondary" style={{ fontSize: 12 }}>超时(分)</Text>
                <InputNumber style={{ width: '100%' }} min={1} max={120} value={spec.timeout_minutes ?? 10}
                  onChange={(v) => onChange((prev) => patchFreestyle(prev, si, ji, { timeout_minutes: v ?? 10 }))} />
              </Col>
              <Col span={12}>
                <Text type="secondary" style={{ fontSize: 12 }}>错误策略</Text>
                <Select style={{ width: '100%' }} value={spec.error_policy ?? 'stop'}
                  onChange={(v) => onChange((prev) => patchFreestyle(prev, si, ji, { error_policy: v as ErrorPolicy }))}
                  options={[
                    { value: 'stop', label: 'stop 刹车' },
                    { value: 'retry', label: 'retry 重试' },
                    { value: 'ignore', label: 'ignore 记红不刹' },
                  ]} />
              </Col>
            </Row>

            {/* 远程主机（job 级名字引用）：引擎投影私钥进 Pod，
                步骤脚本里直接 `ssh <名>`；shell 步骤引用 $MP_H_<名大写>_IP/_PORT/_USER */}
            <div>
              <Text type="secondary" style={{ display: 'block', marginBottom: 6, fontSize: 12 }}>
                远程主机（shell 里 ssh &lt;名&gt; 直连）
              </Text>
              <Select
                mode="multiple" allowClear style={{ width: '100%' }} placeholder="不选 = 纯本地执行"
                value={spec.hosts ?? []}
                options={hosts.map((h) => ({ value: h.name, label: `${h.name}（${h.user_name}@${h.ip}）` }))}
                onChange={(v) => onChange((prev) => patchFreestyle(prev, si, ji, { hosts: v.length ? v : undefined }))}
                notFoundContent="先到「主机」页录主机"
              />
            </div>

            <div>
              <Text type="secondary" style={{ display: 'block', marginBottom: 6, fontSize: 12 }}>
                步骤（自上而下执行）
              </Text>
              {steps.map((step, pi) => (
                <StepForm
                  // key 不含 step.name：改名每键换 key = 整卡卸载重建（焦点丢 BODY / IME 组字被打断），
                  // 位置+类型定位——type 原位切换时仍重挂载，StepForm 的 inline 双档态从新步骤重初始化
                  key={`${si}-${ji}-${pi}-${step.type}`}
                  step={step} index={pi} total={steps.length} registries={registries} nsOptions={nsOptions}
                  onChange={(patch) => onChange((prev) => patchStep(prev, si, ji, pi, patch))}
                  onRemove={() => onChange((prev) => removeStep(prev, si, ji, pi))}
                  onMove={(to) => onChange((prev) => moveStep(prev, si, ji, pi, to))}
                />
              ))}
              <Dropdown
                trigger={['click']}
                menu={{
                  items: STEP_ADD_OPTIONS.map((o) => ({ key: o.value, label: o.label })),
                  onClick: ({ key }) => onChange((prev) => addStep(prev, si, ji, key as StepType)),
                }}
              >
                <Button size="small" icon={<PlusOutlined />}>添加步骤</Button>
              </Dropdown>
            </div>
          </>
        ) : (
          <Space direction="vertical" size={10} style={{ width: '100%' }}>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>环境</Text>
              <Select
                showSearch style={{ width: '100%' }} placeholder="选 env"
                value={job.deploy_spec?.env || undefined}
                options={envs.map((e) => ({ value: e.name, label: `${e.name} → ${e.namespace}` }))}
                onChange={(v) => onChange((prev) => patchDeploy(prev, si, ji, { env: v }))}
                notFoundContent="先到「环境」页建 env"
              />
            </div>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>Deployment</Text>
              <Input placeholder="my-app" value={job.deploy_spec?.deployment_name}
                onChange={(e) => onChange((prev) => patchDeploy(prev, si, ji, { deployment_name: e.target.value }))} />
            </div>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>容器名</Text>
              <Input placeholder="main" value={job.deploy_spec?.container_name}
                onChange={(e) => onChange((prev) => patchDeploy(prev, si, ji, { container_name: e.target.value }))} />
            </div>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>镜像（可插变量）</Text>
              <Input placeholder='{{.build.IMG}} 或字面量' value={job.deploy_spec?.image}
                onChange={(e) => onChange((prev) => patchDeploy(prev, si, ji, { image: e.target.value }))} />
            </div>
            <div>
              <Text type="secondary" style={{ fontSize: 12, marginRight: 8 }}>等 rollout</Text>
              <Switch checked={job.deploy_spec?.wait_rollout ?? true}
                onChange={(v) => onChange((prev) => patchDeploy(prev, si, ji, { wait_rollout: v }))} />
            </div>
          </Space>
        )}
      </Space>
    </Drawer>
  )
}
