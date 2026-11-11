import { useState } from 'react'
import { Button, Drawer, Input, Popconfirm, Space, Tag, Typography } from 'antd'
import { BranchesOutlined, CodeOutlined, DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { clearSource, repoTail, withSource } from './tree'
import type { SourceSpec, Workflow } from '../../types'

const { Text } = Typography

interface Props {
  wf: Workflow
  sourceErr?: string // collectErrors 红标（缺仓库地址）
  onChange: (update: (prev: Workflow) => Workflow) => void // PR #62 更新式契约
}

// 统一代码源列（决断 #18）：画布最左的独立一级节点——
// clone 是流水线的声明而非某个 job 的步骤，任务开跑先克隆进共享工作区（决断 #17），
// 下游 job 直接见文件。卡片 = 仓库尾段 + 分支 chip；点开抽屉编辑。
// git 步骤类型保留（存量图纸零破坏 + 多仓边角），本列是推荐路径。
export default function SourceColumn({ wf, sourceErr, onChange }: Props) {
  const [open, setOpen] = useState(false)
  const src = wf.source

  const patch = (p: Partial<SourceSpec>) =>
    onChange((prev) => withSource(prev, { repo: '', branch: '', token: '', dir: '', ...(prev.source ?? {}), ...p }))

  return (
    <div
      style={{
        width: 250, flexShrink: 0, borderRadius: 8, padding: 10, background: '#fafafa',
        border: sourceErr ? '1px solid #ff4d4f' : '1px solid #e0e0e0',
        display: 'flex', flexDirection: 'column',
      }}
    >
      <div style={{ marginBottom: 10 }}>
        <Space size={6}>
          <CodeOutlined />
          <Text strong style={{ fontSize: 13 }}>代码源</Text>
        </Space>
      </div>

      {src ? (
        <div
          style={{
            background: '#fff', border: '1px solid #e5e7eb', borderRadius: 8, padding: '10px 12px',
            cursor: 'pointer',
          }}
          onClick={() => setOpen(true)}
        >
          <Space size={8} style={{ width: '100%' }}>
            <BranchesOutlined style={{ color: '#13c2c2' }} />
            <Text strong style={{ fontSize: 13 }}>{repoTail(src.repo) || '（未填仓库）'}</Text>
          </Space>
          <div style={{ marginTop: 6, paddingLeft: 22 }}>
            <Tag style={{ marginRight: 0 }} color="cyan">⑂ {src.branch || 'main'}</Tag>
            {src.dir && <Text type="secondary" style={{ fontSize: 11, marginLeft: 6 }}>{src.dir}/</Text>}
          </div>
        </div>
      ) : (
        <Button
          size="small" type="dashed" block icon={<PlusOutlined />}
          style={{ color: sourceErr ? '#ff4d4f' : '#8c8c8c' }}
          onClick={() => setOpen(true)}
        >
          添加代码源
        </Button>
      )}
      {sourceErr && <Text type="danger" style={{ fontSize: 12, marginTop: 6 }}>{sourceErr}</Text>}
      <Text type="secondary" style={{ fontSize: 11, marginTop: 8 }}>
        克隆一次进任务工作区，stage 的 job 直接用；job 里不用再放 git 步骤
      </Text>

      <Drawer
        open={open} width={480} onClose={() => setOpen(false)}
        title={<Space><BranchesOutlined />代码源</Space>}
      >
        <Space direction="vertical" size={14} style={{ width: '100%' }}>
          <div>
            <Text type="secondary" style={{ display: 'block', marginBottom: 4, fontSize: 12 }}>仓库 *</Text>
            <Input
              placeholder="https://github.com/o/r.git" value={src?.repo ?? ''}
              onChange={(e) => patch({ repo: e.target.value })}
            />
          </div>
          <div>
            <Text type="secondary" style={{ display: 'block', marginBottom: 4, fontSize: 12 }}>分支</Text>
            <Input
              placeholder="默认 main" value={src?.branch ?? ''}
              onChange={(e) => patch({ branch: e.target.value })}
            />
          </div>
          <div>
            <Text type="secondary" style={{ display: 'block', marginBottom: 4, fontSize: 12 }}>令牌（私有仓库）</Text>
            <Input.Password
              placeholder="user:pass 或纯 token，公开仓库留空" value={src?.token ?? ''}
              onChange={(e) => patch({ token: e.target.value })}
            />
          </div>
          <div>
            <Text type="secondary" style={{ display: 'block', marginBottom: 4, fontSize: 12 }}>检出目录</Text>
            <Input
              placeholder="留空 = 从 URL 尾段派生" value={src?.dir ?? ''}
              onChange={(e) => patch({ dir: e.target.value })}
            />
            <Text type="secondary" style={{ display: 'block', marginTop: 4, fontSize: 11 }}>
              任务开跑先克隆进共享工作区 /workspace；下游 job 开箱即 $MP_SOURCE_DIR 指向这里
            </Text>
          </div>
          {src && (
            <Popconfirm
              title="移除代码源？（stage 的 job 将各自面对空工作区）"
              onConfirm={() => { onChange(clearSource); setOpen(false) }}
            >
              <Button danger icon={<DeleteOutlined />}>移除代码源</Button>
            </Popconfirm>
          )}
        </Space>
      </Drawer>
    </div>
  )
}
