import { create } from 'zustand'

export interface TerminalEntry {
  id: number
  timestamp: Date
  eventType: string
  payload: string
}

const MAX_ENTRIES = 500

interface TerminalState {
  entries: TerminalEntry[]
  nextId: number
  addEntry: (eventType: string, payload: unknown) => void
  clear: () => void
}

export const useTerminalStore = create<TerminalState>((set) => ({
  entries: [],
  nextId: 1,
  addEntry: (eventType, payload) =>
    set((s) => {
      const entry: TerminalEntry = {
        id: s.nextId,
        timestamp: new Date(),
        eventType,
        payload:
          typeof payload === 'string'
            ? payload
            : JSON.stringify(payload, null, 2),
      }
      const entries = [...s.entries, entry]
      return {
        entries: entries.length > MAX_ENTRIES ? entries.slice(-MAX_ENTRIES) : entries,
        nextId: s.nextId + 1,
      }
    }),
  clear: () => set({ entries: [] }),
}))
