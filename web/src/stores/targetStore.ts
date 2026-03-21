import { create } from 'zustand'
import type { Target } from '../types/api'

interface TargetState {
  targets: Target[]
  setTargets: (targets: Target[]) => void
  updateTarget: (t: Target) => void
  removeTarget: (id: string) => void
}

export const useTargetStore = create<TargetState>((set) => ({
  targets: [],
  setTargets: (targets) => set({ targets }),
  updateTarget: (t) =>
    set((s) => ({
      targets: s.targets.some((x) => x.id === t.id)
        ? s.targets.map((x) => (x.id === t.id ? t : x))
        : [...s.targets, t],
    })),
  removeTarget: (id) =>
    set((s) => ({
      targets: s.targets.filter((x) => x.id !== id),
    })),
}))
