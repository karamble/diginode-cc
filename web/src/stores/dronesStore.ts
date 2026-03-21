import { create } from 'zustand'

export interface Drone {
  id: string
  mac?: string
  serialNumber?: string
  uasId?: string
  operatorId?: string
  description?: string
  uaType?: string
  manufacturer?: string
  model?: string
  latitude?: number
  longitude?: number
  altitude?: number
  speed?: number
  heading?: number
  verticalSpeed?: number
  pilotLatitude?: number
  pilotLongitude?: number
  rssi?: number
  status: 'UNKNOWN' | 'FRIENDLY' | 'NEUTRAL' | 'HOSTILE'
  source?: string
  faaData?: Record<string, unknown>
  firstSeen?: string
  lastSeen?: string
}

interface DronesState {
  drones: Map<string, Drone>
  setDrones: (drones: Drone[]) => void
  updateDrone: (drone: Partial<Drone> & { id: string }) => void
  removeDrone: (id: string) => void
}

export const useDronesStore = create<DronesState>((set) => ({
  drones: new Map(),
  setDrones: (drones) =>
    set({ drones: new Map(drones.map((d) => [d.id, d])) }),
  updateDrone: (update) =>
    set((state) => {
      const drones = new Map(state.drones)
      const existing = drones.get(update.id)
      drones.set(update.id, { ...existing, ...update } as Drone)
      return { drones }
    }),
  removeDrone: (id) =>
    set((state) => {
      const drones = new Map(state.drones)
      drones.delete(id)
      return { drones }
    }),
}))
