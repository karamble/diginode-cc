import { create } from 'zustand'
import type { AlertEvent } from '../types/api'

interface AlertState {
  events: AlertEvent[]
  unacknowledgedCount: number
  addEvent: (e: AlertEvent) => void
  setEvents: (events: AlertEvent[]) => void
}

export const useAlertStore = create<AlertState>((set) => ({
  events: [],
  unacknowledgedCount: 0,
  addEvent: (e) =>
    set((s) => {
      const events = [e, ...s.events].slice(0, 100)
      return {
        events,
        unacknowledgedCount: events.filter((x) => !x.acknowledged).length,
      }
    }),
  setEvents: (events) =>
    set({
      events,
      unacknowledgedCount: events.filter((x) => !x.acknowledged).length,
    }),
}))
