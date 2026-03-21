import { create } from 'zustand'
import type { Aircraft } from '../types/api'

interface ADSBState {
  aircraft: Map<string, Aircraft>
  enabled: boolean
  setAircraft: (list: Aircraft[]) => void
  updateAircraft: (ac: Aircraft) => void
  removeAircraft: (hex: string) => void
  setEnabled: (enabled: boolean) => void
}

export const useADSBStore = create<ADSBState>((set) => ({
  aircraft: new Map(),
  enabled: false,
  setAircraft: (list) =>
    set({ aircraft: new Map(list.map((a) => [a.hex, a])) }),
  updateAircraft: (ac) =>
    set((s) => {
      const aircraft = new Map(s.aircraft)
      aircraft.set(ac.hex, { ...s.aircraft.get(ac.hex), ...ac })
      return { aircraft }
    }),
  removeAircraft: (hex) =>
    set((s) => {
      const aircraft = new Map(s.aircraft)
      aircraft.delete(hex)
      return { aircraft }
    }),
  setEnabled: (enabled) => set({ enabled }),
}))
