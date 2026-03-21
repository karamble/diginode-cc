import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import api from '../api/client'

interface DroneRow {
  id: string
  droneId: string
  mac?: string
  serialNumber?: string
  uasId?: string
  operatorId?: string
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
  status: 'UNKNOWN' | 'FRIENDLY' | 'NEUTRAL' | 'HOSTILE'
  source?: string
  nodeId?: string
  siteName?: string
  siteColor?: string
  firstSeen?: string
  lastSeen?: string
}

const STATUS_OPTIONS: DroneRow['status'][] = ['UNKNOWN', 'FRIENDLY', 'NEUTRAL', 'HOSTILE']

function statusColor(s: string) {
  switch (s) {
    case 'HOSTILE': return 'badge-hostile'
    case 'FRIENDLY': return 'badge-friendly'
    case 'NEUTRAL': return 'badge-neutral'
    default: return 'badge-unknown'
  }
}

function formatCoord(v: number | undefined): string {
  if (v === undefined || v === 0) return '-'
  return v.toFixed(5)
}

export default function DronesPage() {
  const queryClient = useQueryClient()
  const [expandedId, setExpandedId] = useState<string | null>(null)

  const { data: drones = [], isLoading, error } = useQuery({
    queryKey: ['drones'],
    queryFn: () => api.get<DroneRow[]>('/drones'),
    refetchInterval: 5000,
  })

  const updateStatus = useMutation({
    mutationFn: ({ id, status }: { id: string; status: string }) =>
      api.put(`/drones/${id}/status`, { status }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['drones'] }),
  })

  const deleteDrone = useMutation({
    mutationFn: (id: string) => api.delete(`/drones/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['drones'] }),
  })

  const clearAll = useMutation({
    mutationFn: () => api.post('/drones/clear'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['drones'] }),
  })

  const hostileCount = drones.filter((d: DroneRow) => d.status === 'HOSTILE').length
  const unknownCount = drones.filter((d: DroneRow) => d.status === 'UNKNOWN').length

  return (
    <div className="p-6 space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">
            Drones
            <span className="ml-2 text-sm font-normal text-dark-400">({drones.length})</span>
          </h2>
          <div className="flex gap-3 mt-1 text-xs">
            {hostileCount > 0 && (
              <span className="text-status-hostile">{hostileCount} hostile</span>
            )}
            {unknownCount > 0 && (
              <span className="text-status-unknown">{unknownCount} unknown</span>
            )}
          </div>
        </div>
        <button
          onClick={() => {
            if (drones.length > 0 && confirm('Clear all tracked drones?')) clearAll.mutate()
          }}
          disabled={drones.length === 0}
          className="px-3 py-1.5 text-xs rounded-lg bg-dark-800 text-dark-400 hover:bg-dark-700 hover:text-dark-200 disabled:opacity-40 disabled:cursor-not-allowed transition-colors border border-dark-700/50"
        >
          Clear All
        </button>
      </div>

      {/* Error banner */}
      {error && (
        <div className="px-4 py-2 rounded-lg bg-status-hostile/10 border border-status-hostile/30 text-status-hostile text-sm">
          Failed to load drones: {(error as Error).message}
        </div>
      )}

      {/* Table */}
      <div className="bg-surface rounded-xl border border-dark-700/50 overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-dark-700/50 text-dark-400 text-xs uppercase tracking-wider">
              <th className="text-left px-4 py-3">ID</th>
              <th className="text-left px-4 py-3">MAC</th>
              <th className="text-left px-4 py-3">Status</th>
              <th className="text-left px-4 py-3">Type</th>
              <th className="text-right px-4 py-3">Alt (m)</th>
              <th className="text-right px-4 py-3">Speed</th>
              <th className="text-right px-4 py-3">RSSI</th>
              <th className="text-left px-4 py-3">Site</th>
              <th className="text-left px-4 py-3">Last Seen</th>
              <th className="text-right px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td colSpan={10} className="px-4 py-12 text-center text-dark-500">
                  <div className="flex items-center justify-center gap-2">
                    <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                    </svg>
                    Loading drones...
                  </div>
                </td>
              </tr>
            ) : drones.length === 0 ? (
              <tr>
                <td colSpan={10} className="px-4 py-12 text-center text-dark-500">
                  No drones detected
                </td>
              </tr>
            ) : (
              drones.map((d: DroneRow) => (
                <>
                  <tr
                    key={d.id}
                    className="border-b border-dark-700/30 hover:bg-dark-800/30 cursor-pointer transition-colors"
                    onClick={() => setExpandedId(expandedId === d.id ? null : d.id)}
                  >
                    <td className="px-4 py-2.5 font-mono text-xs text-dark-300">
                      {d.droneId?.slice(0, 10) || d.id?.slice(0, 10)}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs text-dark-400">
                      {d.mac || '-'}
                    </td>
                    <td className="px-4 py-2.5">
                      <span className={`px-2 py-0.5 rounded-full text-xs font-medium ${statusColor(d.status)}`}>
                        {d.status}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-dark-400 text-xs">
                      {d.uaType || d.manufacturer || '-'}
                    </td>
                    <td className="px-4 py-2.5 text-right font-mono text-dark-300 text-xs">
                      {d.altitude?.toFixed(0) ?? '-'}
                    </td>
                    <td className="px-4 py-2.5 text-right font-mono text-dark-300 text-xs">
                      {d.speed?.toFixed(1) ?? '-'}
                    </td>
                    <td className="px-4 py-2.5 text-right font-mono text-dark-300 text-xs">
                      {d.rssi ?? '-'}
                    </td>
                    <td className="px-4 py-2.5 text-dark-400 text-xs">{d.siteName || '-'}</td>
                    <td className="px-4 py-2.5 text-dark-500 text-xs">
                      {d.lastSeen ? new Date(d.lastSeen).toLocaleTimeString() : '-'}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          if (confirm(`Remove drone ${d.droneId || d.id}?`)) {
                            deleteDrone.mutate(d.id)
                          }
                        }}
                        className="text-dark-500 hover:text-status-hostile transition-colors"
                        title="Remove drone"
                      >
                        <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                        </svg>
                      </button>
                    </td>
                  </tr>

                  {/* Expanded detail row */}
                  {expandedId === d.id && (
                    <tr key={`${d.id}-detail`} className="border-b border-dark-700/30 bg-dark-800/20">
                      <td colSpan={10} className="px-6 py-4">
                        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-xs">
                          <div>
                            <span className="text-dark-500 block">Position</span>
                            <span className="text-dark-300 font-mono">
                              {formatCoord(d.lat)}, {formatCoord(d.lon)}
                            </span>
                          </div>
                          <div>
                            <span className="text-dark-500 block">Operator</span>
                            <span className="text-dark-300 font-mono">
                              {formatCoord(d.operatorLat)}, {formatCoord(d.operatorLon)}
                            </span>
                          </div>
                          <div>
                            <span className="text-dark-500 block">Heading / V-Speed</span>
                            <span className="text-dark-300 font-mono">
                              {d.heading?.toFixed(0) ?? '-'}&deg; / {d.verticalSpeed?.toFixed(1) ?? '-'} m/s
                            </span>
                          </div>
                          <div>
                            <span className="text-dark-500 block">Serial / UAS ID</span>
                            <span className="text-dark-300 font-mono text-[11px]">
                              {d.serialNumber || d.uasId || '-'}
                            </span>
                          </div>
                          <div>
                            <span className="text-dark-500 block">Source</span>
                            <span className="text-dark-300">{d.source || '-'}</span>
                          </div>
                          <div>
                            <span className="text-dark-500 block">First Seen</span>
                            <span className="text-dark-300">
                              {d.firstSeen ? new Date(d.firstSeen).toLocaleString() : '-'}
                            </span>
                          </div>
                          <div>
                            <span className="text-dark-500 block">Manufacturer / Model</span>
                            <span className="text-dark-300">
                              {[d.manufacturer, d.model].filter(Boolean).join(' ') || '-'}
                            </span>
                          </div>
                          <div>
                            <span className="text-dark-500 block mb-1">Set Status</span>
                            <div className="flex gap-1">
                              {STATUS_OPTIONS.map((s) => (
                                <button
                                  key={s}
                                  onClick={() => updateStatus.mutate({ id: d.id, status: s })}
                                  className={`px-1.5 py-0.5 rounded text-[10px] font-medium transition-colors ${
                                    d.status === s
                                      ? statusColor(s) + ' ring-1 ring-current'
                                      : 'bg-dark-700/50 text-dark-400 hover:bg-dark-700'
                                  }`}
                                >
                                  {s}
                                </button>
                              ))}
                            </div>
                          </div>
                        </div>
                      </td>
                    </tr>
                  )}
                </>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
