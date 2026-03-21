import { create } from 'zustand'

interface MapState {
  center: [number, number]
  zoom: number
  selectedEntity: { type: string; id: string } | null
  setCenter: (center: [number, number]) => void
  setZoom: (zoom: number) => void
  setSelectedEntity: (entity: { type: string; id: string } | null) => void
}

export const useMapStore = create<MapState>((set) => ({
  center: [47.3769, 8.5417], // Zurich default
  zoom: 5,
  selectedEntity: null,
  setCenter: (center) => set({ center }),
  setZoom: (zoom) => set({ zoom }),
  setSelectedEntity: (selectedEntity) => set({ selectedEntity }),
}))
