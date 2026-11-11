import { Button, Result, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useRouteError } from 'react-router-dom'

// 根路由 errorElement —— 渲染崩溃的白屏兜底（如 JSON 逃生舱灌入畸形图纸）。
// react-router 6 对未捕获渲染错误默认弹英文调试页，这里换成中文体面版：
// 错误信息 + 刷新。路由内组件抛错会冒泡到最近的 errorElement，根路由挂 = 全站兜底。
export default function RouteError() {
  const error = useRouteError() as Error
  return (
    <Result
      status="error"
      title="页面出错了"
      subTitle={
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {error?.message ?? String(error)} —— 数据不会丢，刷新即可回到列表页
        </Typography.Text>
      }
      extra={
        <Button type="primary" icon={<ReloadOutlined />} onClick={() => window.location.reload()}>
          刷新页面
        </Button>
      }
    />
  )
}
