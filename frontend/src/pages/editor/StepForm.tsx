import { useState } from 'react'
import type { ReactNode } from 'react'
import { AutoComplete, Button, Input, Radio, Select, Slider, Space, Typography, Upload } from 'antd'
import { DeleteOutlined, ImportOutlined, ArrowUpOutlined, ArrowDownOutlined } from '@ant-design/icons'
import TermEditor from '../../components/TermEditor'
import type { Registry, StepSpec, StepType } from '../../types'

// 单个步骤的编辑表单 —— 5 种类型共享骨架，按 type 显隐专属字段（后端 StepSpec 就是扁平结构，
// 前端如实呈现：字段即协议，不搞每类型一个子 schema 的过度设计）。
// 布局语言：字段竖排（label 在上、控件全宽）——编辑抽屉 560px 宽，横排五连挤的可读性已被
// 用户否决；脚本/内联 Dockerfile 用终端风编辑面（TermEditor）。

const { Text } = Typography

const STEP_TYPES: { value: StepType; label: string }[] = [
  { value: 'shell', label: 'shell 脚本' },
  { value: 'git', label: 'git 拉代码' },
  { value: 'docker_build', label: '镜像构建' },
  { value: 'junit_report', label: '测试报告' },
  { value: 'archive', label: '产物归档' },
]

// 竖排字段的标准骨架：12px 灰 label 在上 + 全宽控件在下
function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div>
      <Text type="secondary" style={{ display: 'block', marginBottom: 4, fontSize: 12 }}>{label}</Text>
      {children}
      {hint && <Text type="secondary" style={{ display: 'block', marginTop: 4, fontSize: 11 }}>{hint}</Text>}
    </div>
  )
}

// splitImage/joinImage —— image 字段的 UI 拆分观感（协议仍是单字段 ns/name，首个 / 切分，
// a/b/c 与 {{var}}/name 均无损 roundtrip；决断 #16 后 image 是相对路径，registry 地址由
// 推送目标解析拼，见 engine/exec/registry.go 三档）。ns 空存裸名（自建 registry 根路径档）
function splitImage(image = ''): [string, string] {
  const i = image.indexOf('/')
  return i === -1 ? ['', image] : [image.slice(0, i), image.slice(i + 1)]
}
function joinImage(ns: string, name: string): string {
  return ns ? `${ns}/${name}` : name
}

interface Props {
  step: StepSpec
  index: number
  total: number
  registries: Registry[] // 仓库下拉数据源（docker_build 目标仓库）：编辑器页拉一次下传，不再每个步骤各拉
  nsOptions: string[] // 命名空间建议（同 job 其余 docker_build 步骤用过的）——服务端无 namespace 数据源，图纸内自洽
  onChange: (patch: Partial<StepSpec>) => void
  onRemove: () => void
  onMove: (to: number) => void
}

export default function StepForm({ step, index, total, registries, nsOptions, onChange, onRemove, onMove }: Props) {
  // 镜像名拆分：ns 由 AutoComplete 选/填、name 手填（compose/parse 见 splitImage/joinImage）
  const [imgNs, imgName] = splitImage(step.image ?? '')
  // Dockerfile 双档：仓库内路径 / 内联编写。档位是纯前端观感态（协议里 content 非空 = 内联赢），
  // 本地 state 驱动 Radio——内联编辑中途清空不弹回路径档，回落语义交给提示文案
  const [inline, setInline] = useState(!!step.dockerfile_content)

  // 本地导入（纯前端 FileReader，不上传）：文件内容原样读进编辑面，可继续手改
  const readInto = (key: 'script' | 'dockerfile_content') => (file: File) => {
    const reader = new FileReader()
    reader.onload = () => onChange({ [key]: String(reader.result ?? '') } as Partial<StepSpec>)
    reader.readAsText(file)
    return false // 阻止 antd Upload 自行上传
  }
  const importScript = readInto('script')
  const importDockerfile = readInto('dockerfile_content')

  return (
    <div style={{ border: '1px solid #e5e7eb', borderRadius: 8, padding: '10px 14px', marginBottom: 10 }}>
      <Space size={10} align="center" style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 10 }}>
        <Space size={10} align="center" style={{ flex: 1 }}>
          <Input
            placeholder="步骤名" value={step.name} style={{ width: 180 }}
            onChange={(e) => onChange({ name: e.target.value })}
            addonBefore={`#${index + 1}`}
          />
          <Select
            style={{ width: 150 }}
            value={step.type} options={STEP_TYPES}
            onChange={(t) => onChange({ type: t })}
          />
        </Space>
        <Button.Group size="small">
          <Button icon={<ArrowUpOutlined />} disabled={index === 0} onClick={() => onMove(index - 1)} />
          <Button icon={<ArrowDownOutlined />} disabled={index === total - 1} onClick={() => onMove(index + 1)} />
          <Button danger icon={<DeleteOutlined />} onClick={onRemove} />
        </Button.Group>
      </Space>

      {step.type === 'shell' && (
        <>
          <TermEditor
            rows={7} value={step.script} placeholder={'#!/bin/sh\necho hello'}
            title={`sh — ${step.name || 'step'}`} lang="sh"
            onChange={(v) => onChange({ script: v })}
          />
          <div style={{ marginTop: 6 }}>
            <Upload accept=".sh,.txt,.py,.yaml,.yml,.json" showUploadList={false} beforeUpload={importScript}>
              <Button size="small" icon={<ImportOutlined />}>导入本地脚本</Button>
            </Upload>
            <Text type="secondary" style={{ marginLeft: 10, fontSize: 12 }}>
              文件内容原样读入脚本框，可继续手写修改
            </Text>
          </div>
        </>
      )}

      {step.type === 'git' && (
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <Field label="仓库">
            <Input placeholder="https://github.com/o/r.git" value={step.repo} onChange={(e) => onChange({ repo: e.target.value })} />
          </Field>
          <Space size={10} style={{ display: 'flex', width: '100%' }}>
            <div style={{ flex: 1 }}>
              <Text type="secondary" style={{ display: 'block', marginBottom: 4, fontSize: 12 }}>分支</Text>
              <Input placeholder="main" value={step.branch} onChange={(e) => onChange({ branch: e.target.value })} />
            </div>
            <div style={{ flex: 1 }}>
              <Text type="secondary" style={{ display: 'block', marginBottom: 4, fontSize: 12 }}>目录</Text>
              <Input placeholder="自动" value={step.context_dir} onChange={(e) => onChange({ context_dir: e.target.value })} />
            </div>
          </Space>
          <Field label="令牌（私有仓库）">
            <Input.Password placeholder="user:pass 或纯 token，公开仓库留空" value={step.token} onChange={(e) => onChange({ token: e.target.value })} />
          </Field>
        </Space>
      )}

      {step.type === 'docker_build' && (
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <Field label="目标仓库" hint="留空 = 推默认仓库（「镜像仓库」页设默认；未设默认运行时任务会报错点名）；推外部仓库先到该页登记">
            <Select
              style={{ width: '100%' }} value={step.registry ?? ''}
              onChange={(v) => onChange({ registry: v || undefined })}
              options={[
                { value: '', label: '跟随默认仓库' },
                ...registries.map((r) => ({
                  value: r.name, label: r.name + (r.is_default ? '（默认）' : ''),
                })),
              ]}
            />
          </Field>
          <Field label="镜像名" hint="命名空间可选：推 ACR 填命名空间（如 codepipeline-learning），自建 registry 留空推根路径">
            <div style={{ display: 'flex', alignItems: 'center' }}>
              <AutoComplete
                style={{ flex: '0 0 150px' }} placeholder="命名空间"
                value={imgNs} options={nsOptions.map((n) => ({ value: n }))}
                onChange={(v) => onChange({ image: joinImage(v, imgName) })}
              />
              <Text type="secondary" style={{ padding: '0 8px' }}>/</Text>
              <Input style={{ flex: 1 }} placeholder="minio-test" value={imgName}
                onChange={(e) => onChange({ image: joinImage(imgNs, e.target.value) })} />
            </div>
          </Field>
          <Field label="tag" hint="留空 = latest">
            <Input placeholder="latest" value={step.tag} onChange={(e) => onChange({ tag: e.target.value })} />
          </Field>
          <Field label="Dockerfile">
            <Radio.Group
              size="small"
              value={inline ? 'inline' : 'path'}
              onChange={(e) => {
                setInline(e.target.value === 'inline')
                if (e.target.value !== 'inline') onChange({ dockerfile_content: undefined })
              }}
            >
              <Radio.Button value="path">仓库内路径</Radio.Button>
              <Radio.Button value="inline">内联编写</Radio.Button>
            </Radio.Group>
            {inline ? (
              <>
                <div style={{ marginTop: 8 }}>
                  <TermEditor
                    rows={8} value={step.dockerfile_content ?? ''} placeholder={'FROM golang:1.26-alpine\nWORKDIR /src\nRUN go build ./...'}
                    title={`Dockerfile — ${step.name || 'step'}`} lang="dockerfile"
                    onChange={(v) => onChange({ dockerfile_content: v })}
                  />
                </div>
                <div style={{ marginTop: 6 }}>
                  <Upload showUploadList={false} beforeUpload={importDockerfile}>
                    <Button size="small" icon={<ImportOutlined />}>导入本地 Dockerfile</Button>
                  </Upload>
                  <Text type="secondary" style={{ marginLeft: 10, fontSize: 12 }}>
                    内联优先于路径档；清空内联内容即回落仓库内路径
                  </Text>
                </div>
              </>
            ) : (
              <Input
                style={{ marginTop: 8 }} addonBefore="路径"
                placeholder="Dockerfile" value={step.dockerfile}
                onChange={(e) => onChange({ dockerfile: e.target.value })}
              />
            )}
          </Field>
          <Field label="构建上下文" hint="相对当前目录（git 步骤 cd 进仓库后即源码根）；留空 = .">
            <Input placeholder="." value={step.context_dir} onChange={(e) => onChange({ context_dir: e.target.value })} />
          </Field>
        </Space>
      )}

      {step.type === 'junit_report' && (
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <Field label="报告文件（glob）">
            <Input placeholder="*.xml" value={step.report_glob} onChange={(e) => onChange({ report_glob: e.target.value })} />
          </Field>
          <Field label={`门禁通过率：${Math.round((step.pass_rate ?? 1) * 100)}%`}>
            <Slider
              min={0} max={1} step={0.05} value={step.pass_rate ?? 1}
              onChange={(v) => onChange({ pass_rate: v })}
              tooltip={{ formatter: (v) => `${Math.round((v ?? 1) * 100)}%` }}
            />
          </Field>
        </Space>
      )}

      {step.type === 'archive' && (
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <Field label="产物（glob）">
            <Input placeholder="dist/*.tgz" value={step.artifact_glob} onChange={(e) => onChange({ artifact_glob: e.target.value })} />
          </Field>
          <Field label="归档名" hint="留空 = <步骤名>.tgz；tar 后上传引擎 GridFS，详情页可下载">
            <Input placeholder="默认 <步骤名>.tgz" value={step.artifact_name} onChange={(e) => onChange({ artifact_name: e.target.value })} />
          </Field>
        </Space>
      )}
    </div>
  )
}
