import { create } from 'zustand'

export interface MeshNode {
  id: string
  nodeNum: number
  nodeId?: string
  longName?: string
  shortName?: string
  hwModel?: string
  role?: string
  firmwareVersion?: string
  latitude?: number
  longitude?: number
  altitude?: number
  batteryLevel?: number
  voltage?: number
  channelUtilization?: number
  airUtilTx?: number
  temperature?: number
  snr?: number
  rssi?: number
  lastHeard?: string
  isOnline: boolean
}

interface NodesState {
  nodes: Map<string, MeshNode>
  setNodes: (nodes: MeshNode[]) => void
  updateNode: (node: Partial<MeshNode> & { id: string }) => void
  removeNode: (id: string) => void
}

export const useNodesStore = create<NodesState>((set) => ({
  nodes: new Map(),
  setNodes: (nodes) =>
    set({ nodes: new Map(nodes.map((n) => [n.id, n])) }),
  updateNode: (update) =>
    set((state) => {
      const nodes = new Map(state.nodes)
      const existing = nodes.get(update.id)
      nodes.set(update.id, { ...existing, ...update } as MeshNode)
      return { nodes }
    }),
  removeNode: (id) =>
    set((state) => {
      const nodes = new Map(state.nodes)
      nodes.delete(id)
      return { nodes }
    }),
}))
