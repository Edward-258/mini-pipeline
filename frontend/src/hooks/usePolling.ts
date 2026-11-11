import { useEffect, useRef, useState } from 'react'
import type { DependencyList } from 'react'

// 轮询 hook —— 语义搬自旧前端 auto()：挂载立即拉一次，之后按间隔刷新，卸载/失能即停。
// 用 setTimeout 链而非 setInterval：慢请求不会重叠堆积（2s 间隔 < 请求耗时时的护栏）。
//
// deps 选项（可选）：fetcher 闭包里的过滤/分页参数列表。依赖变化 → 旧循环作废（在途请求
// 结果不落地）→ 立即重拉，不等下一拍定时器——翻页/切过滤的"点完没反应等 5s"就是缺它。
// 不传 = 纯定时刷新（老用法零变化）。deps 每渲染都是新数组引用，序列化成字符串进 effect
// 依赖数组，只有真实值变化才重启循环。
//
// loading 语义：挂载/依赖变化的"用户在等的那一拍"为 true，定时器续拍静默——表格 loading
// 不会每 5s 闪一下。
export function usePolling<T>(
  fetcher: () => Promise<T>,
  opts: { interval?: number; enabled?: boolean; deps?: DependencyList } = {},
) {
  const { interval = 2000, enabled = true } = opts
  const [data, setData] = useState<T>()
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(false)
  const depsKey = JSON.stringify(opts.deps ?? [])
  const fnRef = useRef(fetcher)

  // 最新闭包同步放 effect 期（render 期写 ref 违反 Rules of React，React Compiler 会拒绝优化）
  useEffect(() => {
    fnRef.current = fetcher
  })

  useEffect(() => {
    if (!enabled) return
    let alive = true
    let timer: ReturnType<typeof setTimeout>
    const tick = async (userWaiting: boolean) => {
      if (userWaiting) setLoading(true)
      try {
        const d = await fnRef.current()
        if (alive) {
          setData(d)
          setError(undefined)
        }
      } catch (e) {
        if (alive) setError(e instanceof Error ? e.message : String(e))
      } finally {
        if (alive) {
          if (userWaiting) setLoading(false)
          timer = setTimeout(() => void tick(false), interval)
        }
      }
    }
    void tick(true)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [interval, enabled, depsKey])

  return { data, error, loading, setData }
}
