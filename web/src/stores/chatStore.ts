import { create } from 'zustand'
import type { ChatMessage } from '../types/api'

interface ChatState {
  messages: ChatMessage[]
  addMessage: (msg: ChatMessage) => void
  setMessages: (msgs: ChatMessage[]) => void
  clearMessages: () => void
}

export const useChatStore = create<ChatState>((set) => ({
  messages: [],
  addMessage: (msg) =>
    set((s) => ({
      messages: [...s.messages, msg].slice(-500),
    })),
  setMessages: (messages) => set({ messages }),
  clearMessages: () => set({ messages: [] }),
}))
