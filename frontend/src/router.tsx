import { createBrowserRouter } from 'react-router-dom'
import App from './App'
import RouteError from './components/RouteError'
import WorkflowList from './pages/workflows/WorkflowList'
import WorkflowEditor from './pages/editor/WorkflowEditor'
import TaskList from './pages/tasks/TaskList'
import TaskDetail from './pages/tasks/TaskDetail'
import EnvList from './pages/envs/EnvList'
import RegistryList from './pages/registries/RegistryList'
import HostList from './pages/hosts/HostList'
import NotifyList from './pages/notifies/NotifyList'
import CronList from './pages/crons/CronList'

export const router = createBrowserRouter([
  {
    path: '/',
    element: <App />,
    // 全站渲染崩溃兜底（JSON 逃生舱灌畸形图纸等）——替换 react-router 默认英文调试页
    errorElement: <RouteError />,
    children: [
      { index: true, element: <WorkflowList /> },
      { path: 'workflows', element: <WorkflowList /> },
      // 编辑器：new = 空白图纸；:name = 加载现有图纸（PR-C 填充实现）
      { path: 'workflows/new', element: <WorkflowEditor /> },
      { path: 'workflows/:name/edit', element: <WorkflowEditor /> },
      { path: 'tasks', element: <TaskList /> },
      { path: 'tasks/:wf/:id', element: <TaskDetail /> },
      { path: 'envs', element: <EnvList /> },
      { path: 'registries', element: <RegistryList /> },
      { path: 'hosts', element: <HostList /> },
      { path: 'notifies', element: <NotifyList /> },
      { path: 'crons', element: <CronList /> },
    ],
  },
])
