import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from '../api/client'

interface Device {
  id: string
  mac: string
  manufacturer?: string
  deviceName?: string
  deviceType?: string
  rssi?: number
  lastSsid?: string
  firstSeen: string
  lastSeen: string
  isKnown: boolean
  notes?: string
  hits: number
  minRssi?: number
  maxRssi?: number
  avgRssi?: number
  lastNodeId?: string
  lastLat?: number
  lastLon?: number
}

function RSSIBar({ rssi }: { rssi: number | undefined }) {
  if (rssi === undefined || rssi === 0) {
    return <span className="text-xs text-dark-500">-</span>
  }

  // RSSI typically ranges from -100 (worst) to -20 (best)
  const clamped = Math.max(-100, Math.min(-20, rssi))
  const pct = ((clamped + 100) / 80) * 100

  let color = 'bg-red-500'
  if (rssi > -50) color = 'bg-green-500'
  else if (rssi > -70) color = 'bg-yellow-500'

  return (
    <div className="flex items-center gap-2">
      <div className="w-16 h-2 bg-dark-700 rounded-full overflow-hidden">
        <div className={`h-full ${color} rounded-full`} style={{ width: `${pct}%` }} />
      </div>
      <span className="text-xs text-dark-400 font-mono w-10 text-right">{rssi}</span>
    </div>
  )
}

function formatDate(dateStr: string) {
  const d = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - d.getTime()
  const diffMin = Math.floor(diffMs / 60000)

  if (diffMin < 1) return 'Just now'
  if (diffMin < 60) return `${diffMin}m ago`
  if (diffMin < 1440) return `${Math.floor(diffMin / 60)}h ago`
  return d.toLocaleDateString()
}

export default function InventoryPage() {
  const queryClient = useQueryClient()

  const { data: devices, isLoading, error } = useQuery<Device[]>({
    queryKey: ['inventory'],
    queryFn: () => api.get('/inventory'),
    refetchInterval: 10000,
  })

  const clearMutation = useMutation({
    mutationFn: () => api.post('/inventory/clear'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['inventory'] }),
  })

  const promoteMutation = useMutation({
    mutationFn: (mac: string) => api.post(`/inventory/${mac}/promote`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['inventory'] }),
  })

  // Sort by lastSeen descending
  const sorted = devices ? [...devices].sort((a, b) =>
    new Date(b.lastSeen).getTime() - new Date(a.lastSeen).getTime()
  ) : []

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">Device Inventory</h2>
          <p className="text-sm text-dark-400 mt-1">
            {sorted.length} device{sorted.length !== 1 ? 's' : ''} tracked
            {sorted.filter(d => d.isKnown).length > 0 && (
              <span> ({sorted.filter(d => d.isKnown).length} known)</span>
            )}
          </p>
        </div>
        <button
          onClick={() => {
            if (confirm('Clear all inventory devices?')) clearMutation.mutate()
          }}
          disabled={clearMutation.isPending}
          className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-300 text-sm rounded font-medium transition-colors"
        >
          Clear All
        </button>
      </div>

      <div className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
        {isLoading ? (
          <div className="p-8 text-center">
            <div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
            <p className="mt-2 text-sm text-dark-400">Loading inventory...</p>
          </div>
        ) : error ? (
          <div className="p-8 text-center">
            <p className="text-sm text-red-400">Failed to load inventory: {(error as Error).message}</p>
          </div>
        ) : sorted.length === 0 ? (
          <div className="p-8 text-center">
            <p className="text-sm text-dark-400">No devices in inventory</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">MAC</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Manufacturer</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Type</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">RSSI</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Hits</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">First Seen</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Last Seen</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Known</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {sorted.map((dev) => (
                  <tr key={dev.mac} className="hover:bg-dark-800/30 transition-colors">
                    <td className="px-4 py-3">
                      <span className="text-sm text-dark-200 font-mono">{dev.mac}</span>
                      {dev.deviceName && (
                        <div className="text-xs text-dark-500 mt-0.5">{dev.deviceName}</div>
                      )}
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-300">{dev.manufacturer || '-'}</td>
                    <td className="px-4 py-3 text-sm text-dark-300">{dev.deviceType || '-'}</td>
                    <td className="px-4 py-3">
                      <RSSIBar rssi={dev.rssi} />
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-300 text-right font-mono">{dev.hits}</td>
                    <td className="px-4 py-3 text-sm text-dark-400">{formatDate(dev.firstSeen)}</td>
                    <td className="px-4 py-3 text-sm text-dark-400">{formatDate(dev.lastSeen)}</td>
                    <td className="px-4 py-3">
                      {dev.isKnown ? (
                        <span className="inline-flex px-2 py-0.5 text-xs font-medium rounded bg-green-600/20 text-green-400 border border-green-500/30">Known</span>
                      ) : (
                        <span className="text-xs text-dark-500">-</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-right">
                      <button
                        onClick={() => promoteMutation.mutate(dev.mac)}
                        disabled={promoteMutation.isPending}
                        className="text-xs text-primary-400 hover:text-primary-300 transition-colors"
                        title="Promote to target"
                      >
                        Promote
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
