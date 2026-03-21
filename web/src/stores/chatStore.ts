import { create } from 'zustand'
import type { ChatMessage } from '../types/api'

type ActiveChat = { mode: 'broadcast' } | { mode: 'dm'; peerNodeNum: number }

interface ChatState {
  messages: ChatMessage[]
  activeChat: ActiveChat
  unreadDMs: Record<number, number>
  addMessage: (msg: ChatMessage) => void
  setMessages: (msgs: ChatMessage[]) => void
  clearMessages: () => void
  setActiveChat: (chat: ActiveChat) => void
  incrementUnread: (nodeNum: number) => void
  clearUnread: (nodeNum: number) => void
}

const BROADCAST_ADDR = 0xFFFFFFFF

export const useChatStore = create<ChatState>((set) => ({
  messages: [],
  activeChat: { mode: 'broadcast' },
  unreadDMs: {},
  addMessage: (msg) =>
    set((s) => ({
      messages: [...s.messages, msg].slice(-500),
    })),
  setMessages: (messages) => set({ messages }),
  clearMessages: () => set({ messages: [] }),
  setActiveChat: (activeChat) => set({ activeChat }),
  incrementUnread: (nodeNum) =>
    set((s) => ({
      unreadDMs: { ...s.unreadDMs, [nodeNum]: (s.unreadDMs[nodeNum] || 0) + 1 },
    })),
  clearUnread: (nodeNum) =>
    set((s) => {
      const { [nodeNum]: _, ...rest } = s.unreadDMs
      return { unreadDMs: rest }
    }),
}))

export { BROADCAST_ADDR }
