import { useState, useRef, useEffect, useCallback } from 'react'
import { agentChat, AgentChatResponse, AgentAction, getAgentConfig, updateAgentConfig } from '../services/api'

interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
  actions?: AgentAction[]
  loading?: boolean
}

export default function AgentChat() {
  const [open, setOpen] = useState(false)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [showConfig, setShowConfig] = useState(false)
  const [configLoading, setConfigLoading] = useState(false)
  const [configForm, setConfigForm] = useState({ llm_base_url: '', llm_api_key: '', llm_model: '' })
  const [configStatus, setConfigStatus] = useState<'unconfigured' | 'configured' | 'error'>('unconfigured')
  const bottomRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const loadConfig = useCallback(async () => {
    try {
      const cfg = await getAgentConfig()
      setConfigStatus(cfg.configured ? 'configured' : 'unconfigured')
    } catch {
      setConfigStatus('error')
    }
  }, [])

  useEffect(() => {
    loadConfig()
  }, [loadConfig])

  const saveConfig = useCallback(async () => {
    setConfigLoading(true)
    try {
      await updateAgentConfig(configForm)
      setConfigStatus('configured')
      setShowConfig(false)
    } catch {
      setConfigStatus('error')
    } finally {
      setConfigLoading(false)
    }
  }, [configForm])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  useEffect(() => {
    if (open) inputRef.current?.focus()
  }, [open])

  const sendMessage = useCallback(async () => {
    const msg = input.trim()
    if (!msg || loading) return

    setInput('')
    setMessages(prev => [...prev, { role: 'user', content: msg }])
    setLoading(true)

    // Add loading placeholder
    setMessages(prev => [...prev, { role: 'assistant', content: '', loading: true }])

    try {
      const resp: AgentChatResponse = await agentChat(msg)
      setMessages(prev => {
        const next = prev.filter(m => !m.loading)
        next.push({ role: 'assistant', content: resp.reply, actions: resp.actions })
        return next
      })
    } catch (err: unknown) {
      const errMsg = err instanceof Error ? err.message : 'Unknown error'
      setMessages(prev => {
        const next = prev.filter(m => !m.loading)
        next.push({ role: 'assistant', content: `Error: ${errMsg}` })
        return next
      })
    } finally {
      setLoading(false)
    }
  }, [input, loading])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      sendMessage()
    }
  }

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="fixed bottom-6 left-6 z-50 w-12 h-12 rounded-full bg-indigo-600 text-white shadow-lg hover:bg-indigo-700 flex items-center justify-center transition-all"
        title="Platform Agent"
      >
        <svg xmlns="http://www.w3.org/2000/svg" className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 10h.01M12 10h.01M16 10h.01M9 16H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-5l-5 5v-5z" />
        </svg>
      </button>
    )
  }

  return (
    <div className="fixed bottom-6 left-6 z-50 w-96 h-[32rem] bg-gray-900 border border-gray-700 rounded-xl shadow-2xl flex flex-col overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 bg-gray-800 border-b border-gray-700">
        <div className="flex items-center gap-2">
          <div className={`w-2 h-2 rounded-full animate-pulse ${configStatus === 'configured' ? 'bg-green-400' : configStatus === 'error' ? 'bg-red-400' : 'bg-yellow-400'}`} />
          <span className="text-sm font-semibold text-gray-100">Platform Agent</span>
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={() => setShowConfig((v: boolean) => !v)}
            className="text-gray-400 hover:text-gray-200 transition-colors p-1"
            title="LLM Settings"
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
          </button>
          <button
            onClick={() => setOpen(false)}
            className="text-gray-400 hover:text-gray-200 transition-colors p-1"
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
      </div>

      {/* Config Panel */}
      {showConfig && (
        <div className="p-4 border-b border-gray-700 bg-gray-850 space-y-3">
          <p className="text-xs text-gray-400">Configure the LLM backend for Platform Agent.</p>
          <div>
            <label className="block text-xs text-gray-400 mb-1">Base URL</label>
            <input
              type="text"
              value={configForm.llm_base_url}
              onChange={e => setConfigForm((f: typeof configForm) => ({ ...f, llm_base_url: e.target.value }))}
              placeholder="https://api.openai.com/v1"
              className="w-full bg-gray-900 border border-gray-600 rounded px-2 py-1.5 text-sm text-gray-200 placeholder-gray-500 focus:outline-none focus:border-indigo-500"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-400 mb-1">API Key</label>
            <input
              type="password"
              value={configForm.llm_api_key}
              onChange={e => setConfigForm((f: typeof configForm) => ({ ...f, llm_api_key: e.target.value }))}
              placeholder="sk-..."
              className="w-full bg-gray-900 border border-gray-600 rounded px-2 py-1.5 text-sm text-gray-200 placeholder-gray-500 focus:outline-none focus:border-indigo-500"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-400 mb-1">Model</label>
            <input
              type="text"
              value={configForm.llm_model}
              onChange={e => setConfigForm((f: typeof configForm) => ({ ...f, llm_model: e.target.value }))}
              placeholder="gpt-4o"
              className="w-full bg-gray-900 border border-gray-600 rounded px-2 py-1.5 text-sm text-gray-200 placeholder-gray-500 focus:outline-none focus:border-indigo-500"
            />
          </div>
          <button
            onClick={saveConfig}
            disabled={configLoading}
            className="w-full px-3 py-1.5 bg-indigo-600 text-white rounded text-sm hover:bg-indigo-700 disabled:opacity-50 transition-colors"
          >
            {configLoading ? 'Saving...' : 'Save Configuration'}
          </button>
        </div>
      )}

      {/* Messages */}
      <div className="flex-1 overflow-y-auto p-3 space-y-3">
        {messages.length === 0 && (
          <div className="text-center text-gray-500 text-sm mt-8">
            <p className="mb-2">👋 Hi! I'm the Platform Agent.</p>
            <p className="text-xs text-gray-600">
              Ask me about projects, challenges, flags, or use commands like "list projects", "submit flag", etc.
            </p>
          </div>
        )}

        {messages.map((msg, i) => (
          <div key={i} className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
            <div
              className={`max-w-[85%] rounded-lg px-3 py-2 text-sm ${
                msg.role === 'user'
                  ? 'bg-indigo-600 text-white'
                  : 'bg-gray-800 text-gray-200 border border-gray-700'
              }`}
            >
              {msg.loading ? (
                <div className="flex items-center gap-1">
                  <div className="w-1.5 h-1.5 bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '0ms' }} />
                  <div className="w-1.5 h-1.5 bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '150ms' }} />
                  <div className="w-1.5 h-1.5 bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '300ms' }} />
                </div>
              ) : (
                <div className="whitespace-pre-wrap break-words">{msg.content}</div>
              )}

              {/* Actions */}
              {msg.actions && msg.actions.length > 0 && (
                <div className="mt-2 pt-2 border-t border-gray-600 space-y-1">
                  {msg.actions.map((action, j) => (
                    <div key={j} className="text-xs text-gray-400 flex items-center gap-1">
                      <span className="text-indigo-400">⚡</span>
                      <span>{action.type}: {action.detail}</span>
                      {action.result && <span className="text-green-400">→ {action.result}</span>}
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        ))}

        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <div className="p-3 border-t border-gray-700 bg-gray-800">
        <div className="flex gap-2">
          <input
            ref={inputRef}
            type="text"
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Ask about projects, challenges..."
            disabled={loading}
            className="flex-1 bg-gray-900 border border-gray-600 rounded-lg px-3 py-2 text-sm text-gray-200 placeholder-gray-500 focus:outline-none focus:border-indigo-500 disabled:opacity-50"
          />
          <button
            onClick={sendMessage}
            disabled={loading || !input.trim()}
            className="px-3 py-2 bg-indigo-600 text-white rounded-lg text-sm hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            Send
          </button>
        </div>
      </div>
    </div>
  )
}
