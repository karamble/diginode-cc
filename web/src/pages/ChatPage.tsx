import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useRef, useEffect } from 'react'
import api from '../api/client'

interface ChatMessage {
  id?: string
  fromNode: number
  toNode: number
  channel: number
  text: string
  timestamp: string
}

function formatNodeId(nodeNum: number): string {
  if (nodeNum === 0 || nodeNum === 0xFFFFFFFF) return 'BROADCAST'
  return `!${nodeNum.toString(16).padStart(8, '0')}`
}

function formatTime(ts: string): string {
  const d = new Date(ts)
  return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function formatDate(ts: string): string {
  const d = new Date(ts)
  const today = new Date()
  if (d.toDateString() === today.toDateString()) return 'Today'
  const yesterday = new Date(today)
  yesterday.setDate(yesterday.getDate() - 1)
  if (d.toDateString() === yesterday.toDateString()) return 'Yesterday'
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

export default function ChatPage() {
  const queryClient = useQueryClient()
  const [message, setMessage] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const { data: messages = [], isLoading } = useQuery({
    queryKey: ['chatMessages'],
    queryFn: () => api.get<ChatMessage[]>('/chat/messages?limit=100'),
    refetchInterval: 3000,
  })

  const sendMessage = useMutation({
    mutationFn: (text: string) => api.post('/serial/text-message', { message: text }),
    onSuccess: () => {
      setMessage('')
      queryClient.invalidateQueries({ queryKey: ['chatMessages'] })
    },
  })

  const clearMessages = useMutation({
    mutationFn: () => api.delete('/chat/messages'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['chatMessages'] }),
  })

  // Auto-scroll to bottom on new messages
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages.length])

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    const text = message.trim()
    if (!text) return
    sendMessage.mutate(text)
  }

  // Reverse to show oldest first (API returns newest first)
  const sortedMessages = [...messages].reverse()

  // Group messages by date
  let lastDate = ''

  return (
    <div className="flex flex-col h-full">
      {/* Terminal header */}
      <div className="px-4 py-3 border-b border-blue-500/20 bg-[#0B1120] flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="text-blue-500 font-mono text-sm font-semibold">MESH TERMINAL</span>
          <span className="text-blue-500/40 font-mono text-xs">
            [{messages.length} msg{messages.length !== 1 ? 's' : ''}]
          </span>
        </div>
        <button
          onClick={() => {
            if (messages.length > 0 && confirm('Clear all chat messages?')) clearMessages.mutate()
          }}
          disabled={messages.length === 0}
          className="px-2 py-1 text-[10px] font-mono rounded bg-blue-500/10 text-blue-500/60 hover:bg-blue-500/20 hover:text-blue-400 disabled:opacity-30 disabled:cursor-not-allowed transition-colors border border-blue-500/20"
        >
          CLEAR
        </button>
      </div>

      {/* Messages area */}
      <div className="flex-1 overflow-y-auto bg-[#0B1120] px-4 py-3 font-mono text-sm">
        {isLoading ? (
          <div className="flex items-center justify-center h-full text-blue-500/40">
            <span className="animate-pulse">Connecting to mesh network...</span>
          </div>
        ) : sortedMessages.length === 0 ? (
          <div className="flex items-center justify-center h-full text-blue-500/30">
            <div className="text-center">
              <div className="text-blue-500/20 text-4xl mb-2">{'>'}_</div>
              <div>No messages yet</div>
              <div className="text-xs mt-1">Messages from the mesh network will appear here</div>
            </div>
          </div>
        ) : (
          <div className="space-y-1">
            {sortedMessages.map((msg, i) => {
              const msgDate = formatDate(msg.timestamp)
              const showDateSep = msgDate !== lastDate
              lastDate = msgDate

              return (
                <div key={msg.id || i}>
                  {/* Date separator */}
                  {showDateSep && (
                    <div className="flex items-center gap-3 my-3">
                      <div className="flex-1 h-px bg-blue-500/10" />
                      <span className="text-blue-500/30 text-[10px] font-mono uppercase tracking-widest">{msgDate}</span>
                      <div className="flex-1 h-px bg-blue-500/10" />
                    </div>
                  )}

                  {/* Message line */}
                  <div className="flex items-start gap-2 py-0.5 hover:bg-blue-500/5 rounded px-1 -mx-1 transition-colors group">
                    {/* Timestamp */}
                    <span className="text-blue-500/30 text-xs flex-shrink-0 mt-0.5 w-16">
                      {formatTime(msg.timestamp)}
                    </span>

                    {/* Node ID */}
                    <span className="text-blue-300 flex-shrink-0 text-xs">
                      {formatNodeId(msg.fromNode)}
                    </span>

                    {/* Channel indicator */}
                    {msg.channel > 0 && (
                      <span className="text-blue-500/40 text-[10px] flex-shrink-0">
                        [ch{msg.channel}]
                      </span>
                    )}

                    {/* Arrow */}
                    <span className="text-blue-500/30 flex-shrink-0">&gt;</span>

                    {/* Target */}
                    {msg.toNode !== 0 && msg.toNode !== 0xFFFFFFFF && (
                      <span className="text-blue-400/50 text-xs flex-shrink-0">
                        @{formatNodeId(msg.toNode)}
                      </span>
                    )}

                    {/* Message text */}
                    <span className="text-blue-400 break-all">{msg.text}</span>
                  </div>
                </div>
              )
            })}
            <div ref={messagesEndRef} />
          </div>
        )}
      </div>

      {/* Input area */}
      <form
        onSubmit={handleSubmit}
        className="px-4 py-3 border-t border-blue-500/20 bg-[#0B1120]"
      >
        <div className="flex items-center gap-2">
          <span className="text-blue-500 font-mono text-sm flex-shrink-0">&gt;</span>
          <input
            ref={inputRef}
            type="text"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            placeholder="Type a message to broadcast..."
            maxLength={237}
            className="flex-1 bg-transparent text-blue-400 font-mono text-sm placeholder-blue-500/30 focus:outline-none caret-blue-500"
            disabled={sendMessage.isPending}
          />
          <button
            type="submit"
            disabled={!message.trim() || sendMessage.isPending}
            className="px-3 py-1 text-xs font-mono rounded bg-blue-500/10 text-blue-500 hover:bg-blue-500/20 hover:text-blue-300 disabled:opacity-30 disabled:cursor-not-allowed transition-colors border border-blue-500/30"
          >
            {sendMessage.isPending ? 'TX...' : 'SEND'}
          </button>
        </div>
        <div className="flex items-center justify-between mt-1.5">
          <span className="text-blue-500/20 font-mono text-[10px]">
            Broadcast to all nodes on mesh
          </span>
          <span className="text-blue-500/20 font-mono text-[10px]">
            {message.length}/237
          </span>
        </div>
      </form>
    </div>
  )
}
