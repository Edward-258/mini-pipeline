import { useState } from 'react'
import type { Key } from 'react'
import { Button, Card, message, Modal, Popconfirm, Space, Table, Tag } from 'antd'
import { PlusOutlined, PlayCircleOutlined, EditOutlined, DeleteOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { usePolling } from '../../hooks/usePolling'
import TimeCell from '../../components/TimeCell'
import { batchDeleteWorkflows, deleteWorkflow, listWorkflows, runWorkflow } from '../../api/resources'
import type { Workflow } from '../../types'

// 工作流列表 —— 入口页：跑一条 / 进编辑器 / 删图纸
// 图纸不是热数据，10s 慢轮询足够（活任务树才需要 2s）
// 0.7.9：checkbox 多选（antd rowSelection，表头全选框 = "全部勾上再删"）+ 批量删除
// （Modal.confirm 确认弹窗——批量/全选删除不可恢复，必须整一个确定界面）
export default function WorkflowList() {
  const nav = useNavigate()
  const { data, error, setData } = usePolling(listWorkflows, { interval: 10000 })
  const [msg, ctx] = message.useMessage()
  const [modal, modalCtx] = Modal.useModal()
  const [selected, setSelected] = useState<Key[]>([])

  const onRun = async (name: string) => {
    try {
      const t = await runWorkflow(name)
      msg.success(`已入队 #${t.task_id}，跳转详情`)
      nav(`/tasks/${name}/${t.task_id}`)
    } catch (e) {
      msg.error((e as Error).message)
    }
  }

  const onDelete = async (name: string) => {
    try {
      await deleteWorkflow(name)
      msg.success(`已删除 ${name}（任务档案与定时器已级联清理）`)
      setData(data?.filter((w) => w.name !== name))
      setSelected((k) => k.filter((x) => x !== name))
    } catch (e) {
      msg.error((e as Error).message)
    }
  }

  // 批量/全选删除：确认弹窗 → 逐条级联删除，部分失败如实点名
  const onDeleteBatch = () => {
    const names = selected.map(String)
    modal.confirm({
      title: `删除选中的 ${names.length} 条工作流？`,
      content: '将级联删除其全部任务档案与定时器，不可恢复。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          const res = await batchDeleteWorkflows(names)
          if (res.failed.length > 0) {
            msg.warning(`已删 ${res.deleted.length} 条，${res.failed.length} 条失败：${res.failed[0].key}（${res.failed[0].error}）`)
          } else {
            msg.success(`已删除 ${res.deleted.length} 条工作流`)
          }
          const gone = new Set(res.deleted)
          setData(data?.filter((w) => !gone.has(w.name)))
          setSelected([])
        } catch (e) {
          msg.error((e as Error).message)
        }
      },
    })
  }

  return (
    <Card
      title="工作流"
      extra={
        <Space>
          <Button danger icon={<DeleteOutlined />} disabled={selected.length === 0} onClick={onDeleteBatch}>
            批量删除{selected.length > 0 ? `（${selected.length}）` : ''}
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => nav('/workflows/new')}>新建</Button>
        </Space>
      }
    >
      {ctx}
      {modalCtx}
      {error && <p style={{ color: '#ef4444' }}>加载失败：{error}</p>}
      <Table<Workflow>
        rowKey="name"
        size="middle"
        loading={!data}
        dataSource={data ?? []}
        pagination={false}
        rowSelection={{ selectedRowKeys: selected, onChange: setSelected }}
        columns={[
          { title: '名称', dataIndex: 'name', render: (v, r) => <a onClick={() => nav(`/workflows/${r.name}/edit`)}>{v}</a> },
          { title: '阶段', render: (_, r) => r.stages?.length ?? 0 },
          {
            title: '闸门/触发', render: (_, r) => (
              <Space size={4}>
                {r.stages?.some((s) => s.manual_exec?.enabled) && <Tag color="orange">审批</Tag>}
                {(r.webhooks?.length ?? 0) > 0 && <Tag color="blue">webhook×{r.webhooks!.length}</Tag>}
                {Object.keys(r.global_context ?? {}).length > 0 && <Tag color="purple">变量</Tag>}
              </Space>
            ),
          },
          { title: '创建时间', dataIndex: 'created_at', render: (v) => <TimeCell value={v} /> },
          {
            title: '操作', width: 240, render: (_, r) => (
              <Space>
                <Button size="small" type="primary" icon={<PlayCircleOutlined />} onClick={() => onRun(r.name)}>运行</Button>
                <Button size="small" icon={<EditOutlined />} onClick={() => nav(`/workflows/${r.name}/edit`)}>编辑</Button>
                <Popconfirm title={`删除图纸 ${r.name}？任务档案与定时器将一并删除`} onConfirm={() => onDelete(r.name)}>
                  <Button size="small" danger>删除</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
    </Card>
  )
}
