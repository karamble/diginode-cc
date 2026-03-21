import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useRef, useEffect } from 'react'
import api from '../api/client'
import { useChatStore, BROADCAST_ADDR } from '../stores/chatStore'

interface ChatMessage {
  id?: string
  fromNode: number
  toNode: number
  channel: number
  text: string
  timestamp: string
}

interface NodeRow {
  nodeNum: number
  nodeType?: string
  name?: string
  shortName?: string
  isOnline: boolean
  isLocal?: boolean
}

function formatNodeId(nodeNum: number): string {
  if (nodeNum === 0 || nodeNum === BROADCAST_ADDR) return 'BROADCAST'
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

function nodeName(n: NodeRow): string {
  return n.name || n.shortName || formatNodeId(n.nodeNum)
}

export default function ChatPage() {
  const queryClient = useQueryClient()
  const [message, setMessage] = useState('')
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const { activeChat, setActiveChat, unreadDMs, clearUnread } = useChatStore()

  // Fetch nodes for sidebar
  const { data: nodes = [] } = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api.get<NodeRow[]>('/nodes'),
    refetchInterval: 10000,
  })

  // Non-local nodes for sidebar
  const remoteNodes = nodes.filter((n: NodeRow) => !n.isLocal)

  // Find peer node info for DM header
  const peerNode = activeChat.mode === 'dm'
    ? nodes.find((n: NodeRow) => n.nodeNum === activeChat.peerNodeNum)
    : null

  // Build query based on active chat
  const chatQueryKey = activeChat.mode === 'broadcast'
    ? ['chatMessages', 'broadcast']
    : ['chatMessages', 'dm', activeChat.peerNodeNum]

  const chatQueryFn = () => {
    if (activeChat.mode === 'broadcast') {
      return api.get<ChatMessage[]>('/chat/messages?limit=100&mode=broadcast')
    }
    return api.get<ChatMessage[]>(`/chat/messages?limit=100&mode=dm&peer=${activeChat.peerNodeNum}`)
  }

  const { data: messages = [], isLoading } = useQuery({
    queryKey: chatQueryKey,
    queryFn: chatQueryFn,
    refetchInterval: 3000,
  })

  const sendMessage = useMutation({
    mutationFn: (text: string) => {
      const body: { message: string; to?: number } = { message: text }
      if (activeChat.mode === 'dm') {
        body.to = activeChat.peerNodeNum
      }
      return api.post('/serial/text-message', body)
    },
    onSuccess: () => {
      setMessage('')
      queryClient.invalidateQueries({ queryKey: chatQueryKey })
    },
  })

  const clearMessages = useMutation({
    mutationFn: () => api.delete('/chat/messages'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: chatQueryKey }),
  })

  // Auto-scroll to bottom on new messages
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages.length])

  // Focus input on chat switch
  useEffect(() => {
    inputRef.current?.focus()
  }, [activeChat])

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    const text = message.trim()
    if (!text) return
    sendMessage.mutate(text)
  }

  const handleSelectBroadcast = () => {
    setActiveChat({ mode: 'broadcast' })
    queryClient.invalidateQueries({ queryKey: ['chatMessages'] })
  }

  const handleSelectNode = (nodeNum: number) => {
    setActiveChat({ mode: 'dm', peerNodeNum: nodeNum })
    clearUnread(nodeNum)
    queryClient.invalidateQueries({ queryKey: ['chatMessages'] })
  }

  // Reverse to show oldest first (API returns newest first)
  const sortedMessages = [...messages].reverse()

  // Group messages by date
  let lastDate = ''

  const headerTitle = activeChat.mode === 'broadcast'
    ? 'MESH TERMINAL'
    : `DM: ${peerNode ? nodeName(peerNode) : formatNodeId(activeChat.peerNodeNum)}`

  const inputPlaceholder = activeChat.mode === 'broadcast'
    ? 'Type a message to broadcast...'
    : `DM to ${peerNode ? nodeName(peerNode) : formatNodeId(activeChat.peerNodeNum)}...`

  const statusLabel = activeChat.mode === 'broadcast'
    ? 'Broadcast to all nodes on mesh'
    : `Direct message to ${peerNode ? nodeName(peerNode) : formatNodeId(activeChat.peerNodeNum)}`

  return (
    <div className="flex h-full">
      {/* Message area */}
      <div className="flex-1 flex flex-col min-w-0">
        {/* Terminal header */}
        <div className="px-4 py-3 border-b border-blue-500/20 bg-[#0B1120] flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-blue-500 font-mono text-sm font-semibold">{headerTitle}</span>
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
                <div className="text-xs mt-1">
                  {activeChat.mode === 'broadcast'
                    ? 'Messages from the mesh network will appear here'
                    : `Start a conversation with ${peerNode ? nodeName(peerNode) : 'this node'}`}
                </div>
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
                    {showDateSep && (
                      <div className="flex items-center gap-3 my-3">
                        <div className="flex-1 h-px bg-blue-500/10" />
                        <span className="text-blue-500/30 text-[10px] font-mono uppercase tracking-widest">{msgDate}</span>
                        <div className="flex-1 h-px bg-blue-500/10" />
                      </div>
                    )}

                    <div className="flex items-start gap-2 py-0.5 hover:bg-blue-500/5 rounded px-1 -mx-1 transition-colors group">
                      <span className="text-blue-500/30 text-xs flex-shrink-0 mt-0.5 w-16">
                        {formatTime(msg.timestamp)}
                      </span>
                      <span className="text-blue-300 flex-shrink-0 text-xs">
                        {msg.fromNode === 0 ? 'YOU' : formatNodeId(msg.fromNode)}
                      </span>
                      {msg.channel > 0 && (
                        <span className="text-blue-500/40 text-[10px] flex-shrink-0">
                          [ch{msg.channel}]
                        </span>
                      )}
                      <span className="text-blue-500/30 flex-shrink-0">&gt;</span>
                      {activeChat.mode === 'broadcast' && msg.toNode !== 0 && msg.toNode !== BROADCAST_ADDR && (
                        <span className="text-blue-400/50 text-xs flex-shrink-0">
                          @{formatNodeId(msg.toNode)}
                        </span>
                      )}
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
              placeholder={inputPlaceholder}
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
              {statusLabel}
            </span>
            <span className="text-blue-500/20 font-mono text-[10px]">
              {message.length}/237
            </span>
          </div>
        </form>
      </div>

      {/* Right sidebar — node list */}
      <div className="w-48 flex-shrink-0 border-l border-blue-500/20 bg-[#0B1120] flex flex-col">
        <div className="px-3 py-3 border-b border-blue-500/20">
          <span className="text-blue-500/60 font-mono text-[10px] uppercase tracking-widest">Channels</span>
        </div>

        <div className="flex-1 overflow-y-auto">
          {/* Broadcast entry */}
          <button
            onClick={handleSelectBroadcast}
            className={`w-full text-left px-3 py-2 flex items-center gap-2 transition-colors border-b border-blue-500/10 ${
              activeChat.mode === 'broadcast'
                ? 'bg-blue-500/10 text-blue-400'
                : 'text-blue-500/50 hover:bg-blue-500/5 hover:text-blue-400/70'
            }`}
          >
            {/* Broadcast icon */}
            <svg className="w-3.5 h-3.5 flex-shrink-0" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M8.288 15.038a5.25 5.25 0 017.424 0M5.106 11.856c3.807-3.808 9.98-3.808 13.788 0M1.924 8.674c5.565-5.565 14.587-5.565 20.152 0" />
            </svg>
            <span className="font-mono text-xs truncate">BROADCAST</span>
          </button>

          {/* Node separator */}
          {remoteNodes.length > 0 && (
            <div className="px-3 py-1.5 border-b border-blue-500/10">
              <span className="text-blue-500/30 font-mono text-[9px] uppercase tracking-widest">Nodes</span>
            </div>
          )}

          {/* Node list */}
          {remoteNodes.map((n: NodeRow) => {
            const unread = unreadDMs[n.nodeNum] || 0
            const isActive = activeChat.mode === 'dm' && activeChat.peerNodeNum === n.nodeNum
            return (
              <button
                key={n.nodeNum}
                onClick={() => handleSelectNode(n.nodeNum)}
                className={`w-full text-left px-3 py-2 flex items-center gap-2 transition-colors border-b border-blue-500/10 ${
                  isActive
                    ? 'bg-blue-500/10 text-blue-400'
                    : 'text-blue-500/50 hover:bg-blue-500/5 hover:text-blue-400/70'
                }`}
              >
                {/* Online dot */}
                <span
                  className={`inline-block w-2 h-2 rounded-full flex-shrink-0 ${
                    n.isOnline ? 'bg-green-500 shadow-[0_0_4px_rgba(34,197,94,0.4)]' : 'bg-gray-600'
                  }`}
                />
                {/* Node name */}
                <span className="font-mono text-xs truncate flex-1">
                  {nodeName(n)}
                </span>
                {/* Unread DM indicator */}
                {unread > 0 && (
                  <span className="flex items-center gap-1 flex-shrink-0">
                    <svg className="w-3 h-3 text-blue-400" fill="currentColor" viewBox="0 0 20 20">
                      <path d="M2.003 5.884L10 9.882l7.997-3.998A2 2 0 0016 4H4a2 2 0 00-1.997 1.884z" />
                      <path d="M18 8.118l-8 4-8-4V14a2 2 0 002 2h12a2 2 0 002-2V8.118z" />
                    </svg>
                    <span className="text-[10px] font-mono text-blue-400">{unread}</span>
                  </span>
                )}
              </button>
            )
          })}
        </div>
      </div>
    </div>
  )
}
