import { Input, Typography } from 'antd'

const { Text } = Typography

interface Props {
  value?: string // 可缺省（StepSpec 的可选字段直传），按空串处理
  onChange: (v: string) => void
  title: string // 标题栏文字（如 sh — build / Dockerfile — img）
  lang?: string // 右上角语言徽标（sh / dockerfile）
  placeholder?: string
  rows?: number
}

// 深色底上的占位符提亮——antd 的占位色是给白底设计的，深色里几乎不可见（一次性注入）
if (typeof document !== 'undefined' && !document.getElementById('mp-term-style')) {
  const s = document.createElement('style')
  s.id = 'mp-term-style'
  s.textContent = '.mp-term textarea::placeholder{color:#6e7681!important}'
  document.head.appendChild(s)
}

const MONO = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'

// 终端风编辑面（shell 脚本 / 内联 Dockerfile 共用）：mac 三圆点标题栏 + 深色等宽正文。
// 纯观感件——值协议与 antd TextArea 同形；teal 光标对齐主题主色（#14b8a6）。
export default function TermEditor({ value = '', onChange, title, lang, placeholder, rows = 8 }: Props) {
  return (
    <div className="mp-term" style={{ border: '1px solid #2b313a', borderRadius: 6, overflow: 'hidden' }}>
      <div
        style={{
          display: 'flex', alignItems: 'center', gap: 6,
          background: '#1b2029', padding: '5px 10px', borderBottom: '1px solid #2b313a',
        }}
      >
        {['#ff5f57', '#febc2e', '#28c840'].map((c) => (
          <span key={c} style={{ width: 9, height: 9, borderRadius: '50%', background: c, display: 'inline-block' }} />
        ))}
        <Text style={{ marginLeft: 8, fontFamily: MONO, fontSize: 12, color: '#9aa4b2' }}>{title}</Text>
        {lang && (
          <span
            style={{
              marginLeft: 'auto', fontFamily: MONO, fontSize: 11, color: '#9aa4b2',
              border: '1px solid #2b313a', borderRadius: 4, padding: '0 6px',
            }}
          >
            {lang}
          </span>
        )}
      </div>
      <Input.TextArea
        variant="borderless"
        rows={rows}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        style={{
          background: '#0d1117', color: '#e6edf3', caretColor: '#14b8a6',
          fontFamily: MONO, fontSize: 12.5, lineHeight: 1.65, resize: 'vertical',
        }}
      />
    </div>
  )
}
