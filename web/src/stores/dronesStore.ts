import { create } from 'zustand'

export type DroneStatus = 'UNKNOWN' | 'FRIENDLY' | 'NEUTRAL' | 'HOSTILE'

export interface Drone {
  id: string
  droneId?: string
  mac?: string
  serialNumber?: string
  uasId?: string
  operatorId?: string
  description?: string
  uaType?: string
  manufacturer?: string
  model?: string
  lat: number
  lon: number
  altitude?: number
  speed?: number
  heading?: number
  verticalSpeed?: number
  operatorLat?: number
  operatorLon?: number
  rssi?: number
  status: DroneStatus
  source?: string
  nodeId?: string
  siteId?: string
  siteName?: string
  siteColor?: string
  faa?: Record<string, unknown>
  firstSeen?: string
  lastSeen?: string
  lastDataAt?: string
}

export interface TrailPoint {
  lat: number
  lon: number
  ts: number
}

const MAX_TRAIL_POINTS = 80

interface DronesState {
  drones: Map<string, Drone>
  trails: Map<string, TrailPoint[]>
  setDrones: (drones: Drone[]) => void
  updateDrone: (drone: Partial<Drone> & { id: string }) => void
  removeDrone: (id: string) => void
  appendTrail: (droneId: string, lat: number, lon: number) => void
  clearTrails: () => void
}

export const useDronesStore = create<DronesState>((set) => ({
  drones: new Map(),
  trails: new Map(),
  setDrones: (drones) =>
    set({ drones: new Map(drones.map((d) => [d.id, d])) }),
  updateDrone: (update) =>
    set((state) => {
      const drones = new Map(state.drones)
      const existing = drones.get(update.id)
      const merged = { ...existing, ...update, lastDataAt: new Date().toISOString() } as Drone
      drones.set(update.id, merged)

      // Append trail point on position update
      const trails = new Map(state.trails)
      if (merged.lat && merged.lon && merged.lat !== 0 && merged.lon !== 0) {
        const trail = trails.get(update.id) || []
        const last = trail[trail.length - 1]
        // Only append if position changed
        if (!last || last.lat !== merged.lat || last.lon !== merged.lon) {
          const updated = [...trail, { lat: merged.lat, lon: merged.lon, ts: Date.now() }]
          trails.set(update.id, updated.length > MAX_TRAIL_POINTS ? updated.slice(-MAX_TRAIL_POINTS) : updated)
        }
      }

      return { drones, trails }
    }),
  removeDrone: (id) =>
    set((state) => {
      const drones = new Map(state.drones)
      const trails = new Map(state.trails)
      drones.delete(id)
      trails.delete(id)
      return { drones, trails }
    }),
  appendTrail: (droneId, lat, lon) =>
    set((state) => {
      const trails = new Map(state.trails)
      const trail = trails.get(droneId) || []
      const last = trail[trail.length - 1]
      if (!last || last.lat !== lat || last.lon !== lon) {
        const updated = [...trail, { lat, lon, ts: Date.now() }]
        trails.set(droneId, updated.length > MAX_TRAIL_POINTS ? updated.slice(-MAX_TRAIL_POINTS) : updated)
      }
      return { trails }
    }),
  clearTrails: () =>
    set({ trails: new Map() }),
}))
