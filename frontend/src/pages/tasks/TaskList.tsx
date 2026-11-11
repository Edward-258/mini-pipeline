import { useState } from 'react'
import type { Key } from 'react'
import { Button, Card, Input, message, Modal, Popconfirm, Select, Space, Table } from 'antd'
import { DeleteOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { usePolling } from '../../hooks/usePolling'
import StatusTag from '../../components/StatusTag'
import TimeCell from '../../components/TimeCell'
import { batchDeleteTasks, listTasks, listWorkflows } from '../../api/resources'
import type { Status, WorkflowTask } from '../../types'

// 任务列表 —— 0.7.9 起默认「全部工作流」总账本（跨库 created_at 降序），下拉可切单库；
// checkbox 多选（跨页累积勾选）+ 批量/单条删除档案。非终态行勾选框置灰（删除只收终态
// ——运行中/暂停中的任务先在详情页取消，前后端双保险，backend 同款守卫）。
const STATUS_OPTIONS: Status[] = ['queued', 'running', 'passed', 'failed', 'cancelled', 'pause']
const TERMINAL: Status[] = ['passed', 'failed', 'cancelled', 'skipped']
const SIZE = 20

export default function TaskList() {
  const nav = useNavigate()
  const [wf, setWf] = useState<string>('') // '' = 全部工作流
  const [status, setStatus] = useState<string>()
  const [creator, setCreator] = useState('')
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<Key[]>([])
  const [msg, ctx] = message.useMessage()
  const [modal, modalCtx] = Modal.useModal()

  const { data: workflows } = usePolling(listWorkflows, { interval: 30000 })
  // deps 带上过滤/页码：依赖变化立即重拉，不等下一拍定时器（原翻页要干等 5s）
  const { data, error, loading } = usePolling(
    () => listTasks(wf, { status, creator, page, size: SIZE }),
    { interval: 5000, deps: [wf, status, creator, page] },
  )

  const dur = (t: WorkflowTask) => {
    if (!t.start_time) return '-'
    const end = t.end_time ?? Math.floor(Date.now() / 1000)
    const s = end - t.start_time
    return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${s % 60}s`
  }

  const onDeleteOne = async (r: WorkflowTask) => {
    try {
      const res = await batchDeleteTasks([r.id])
      if (res.failed.length > 0) {
        msg.error(`删除失败：${res.failed[0].error}`)
        return
      }
      msg.success(`已删除 #${r.task_id}`)
    } catch (e) {
      msg.error((e as Error).message)
    }
  }

  // 批量/全选删除：确认弹窗 → 终态档案逐条删除，跳过项如实点名
  const onDeleteBatch = () => {
    const ids = selected.map(String)
    modal.confirm({
      title: `删除选中的 ${ids.length} 条任务档案？`,
      content: '仅删除终态任务（已通过/失败/取消/跳过），不可恢复。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          const res = await batchDeleteTasks(ids)
          if (res.failed.length > 0) {
            msg.warning(`已删 ${res.deleted.length} 条，${res.failed.length} 条跳过：${res.failed[0].key}（${res.failed[0].error}）`)
          } else {
            msg.success(`已删除 ${res.deleted.length} 条任务档案`)
          }
          setSelected([])
        } catch (e) {
          msg.error((e as Error).message)
        }
      },
    })
  }

  return (
    <Card
      title="任务"
      extra={
        <Button danger icon={<DeleteOutlined />} disabled={selected.length === 0} onClick={onDeleteBatch}>
          批量删除{selected.length > 0 ? `（${selected.length}）` : ''}
        </Button>
      }
    >
      {ctx}
      {modalCtx}
      <Space style={{ marginBottom: 16 }} wrap>
        <Select
          style={{ width: 220 }} showSearch
          value={wf}
          options={[{ value: '', label: '全部工作流' }, ...(workflows ?? []).map((w) => ({ value: w.name, label: w.name }))]}
          onChange={(v) => { setWf(v); setPage(1); setSelected([]) }}
        />
        <Select
          style={{ width: 140 }} placeholder="状态" allowClear
          value={status} onChange={(v) => { setStatus(v); setPage(1) }}
          options={STATUS_OPTIONS.map((s) => ({ value: s, label: s }))}
        />
        <Input.Search
          style={{ width: 200 }} placeholder="creator 过滤" allowClear
          onSearch={(v) => { setCreator(v); setPage(1) }}
        />
      </Space>
      {error && <p style={{ color: '#ef4444' }}>加载失败：{error}</p>}
      <Table<WorkflowTask>
        rowKey="id"
        size="middle"
        loading={!data || loading}
        dataSource={data?.items ?? []}
        rowSelection={{
          selectedRowKeys: selected,
          onChange: setSelected,
          preserveSelectedRowKeys: true, // 跨页累积勾选（"全部删除"= 逐页勾满再点删除）
          getCheckboxProps: (r) => ({ disabled: !TERMINAL.includes(r.status) }), // 非终态置灰：删除只收终态
        }}
        pagination={{
          // 受控 current = 本地 page：点页码即时高亮，不等响应回显（响应里的 page 是请求回声，值恒等）
          current: page, pageSize: SIZE, total: data?.total ?? 0,
          onChange: setPage, showTotal: (t) => `共 ${t} 条`,
        }}
        columns={[
          { title: '#', dataIndex: 'task_id', width: 70, render: (v, r) => <a onClick={() => nav(`/tasks/${r.workflow_name}/${r.task_id}`)}>#{v}</a> },
          { title: '工作流', dataIndex: 'workflow_name' },
          { title: '状态', dataIndex: 'status', width: 100, render: (s) => <StatusTag status={s} /> },
          { title: '触发者', dataIndex: 'creator', width: 160 },
          { title: '开始', dataIndex: 'start_time', width: 170, render: (v) => <TimeCell value={v} /> },
          { title: '耗时', width: 90, render: (_, r) => dur(r) },
          {
            title: '操作', width: 150, render: (_, r) => (
              <Space>
                <Button size="small" onClick={() => nav(`/tasks/${r.workflow_name}/${r.task_id}`)}>详情</Button>
                {TERMINAL.includes(r.status) && (
                  <Popconfirm title={`删除任务档案 #${r.task_id}？`} onConfirm={() => onDeleteOne(r)}>
                    <Button size="small" danger>删除</Button>
                  </Popconfirm>
                )}
              </Space>
            ),
          },
        ]}
      />
    </Card>
  )
}
