import { create } from 'zustand'
import type { Geofence } from '../types/api'

interface GeofenceState {
  geofences: Geofence[]
  setGeofences: (g: Geofence[]) => void
  updateGeofence: (g: Geofence) => void
  removeGeofence: (id: string) => void
}

export const useGeofenceStore = create<GeofenceState>((set) => ({
  geofences: [],
  setGeofences: (geofences) => set({ geofences }),
  updateGeofence: (g) =>
    set((s) => ({
      geofences: s.geofences.some((x) => x.id === g.id)
        ? s.geofences.map((x) => (x.id === g.id ? g : x))
        : [...s.geofences, g],
    })),
  removeGeofence: (id) =>
    set((s) => ({
      geofences: s.geofences.filter((x) => x.id !== id),
    })),
}))
