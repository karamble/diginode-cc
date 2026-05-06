import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import api from '../api/client'

interface BLEDetection {
  id: number
  mac: string
  node_id: string
  rssi: number
  channel: number
  timestamp: string
  detection_type?: string
  manufacturer?: string
  manufacturer_id?: number
  local_name?: string
  appearance?: number
  service_uuids_16?: number[]
  service_uuids_128?: string[]
  tx_power?: number
  is_random_addr: boolean
  raw_adv: string
  classification?: Record<string, unknown>
  findmy_score?: number
  combined_score?: number
}

// detectionBadge picks a colour for the detection_type chip. Trackers and
// surveillance hardware are red; sensors and appliances are amber; everything
// else is teal. Keeps the UI legible at a glance without a legend.
function detectionBadge(t?: string): { color: string; label: string } {
  if (!t) return { color: 'bg-dark-600 text-dark-300 border-dark-500/40', label: 'unknown' }
  const lower = t.toLowerCase()
  const tracker = ['airtag', 'tile', 'samsung_smarttag', 'chipolo', 'pebblebee', 'eufy', 'nut', 'macless_haystack']
  if (tracker.includes(lower)) {
    return { color: 'bg-red-500/20 text-red-300 border-red-500/30', label: lower }
  }
  if (lower.startsWith('surveillance')) {
    return { color: 'bg-red-500/20 text-red-300 border-red-500/30', label: lower.replace('surveillance_', '') }
  }
  if (lower.includes('sensor') || lower === 'domestic_appliance' || lower === 'cookware') {
    return { color: 'bg-amber-500/20 text-amber-300 border-amber-500/30', label: lower }
  }
  return { color: 'bg-teal-500/20 text-teal-300 border-teal-500/30', label: lower }
}

function rssiColor(rssi: number): string {
  if (rssi >= -60) return 'bg-emerald-500/30 text-emerald-300'
  if (rssi >= -75) return 'bg-amber-500/30 text-amber-300'
  return 'bg-red-500/30 text-red-300'
}

function timeAgo(iso: string): string {
  const t = new Date(iso).getTime()
  if (!isFinite(t)) return '-'
  const diff = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

export default function BLEDetectionsPage() {
  const [typeFilter, setTypeFilter] = useState<string>('')
  const [macFilter, setMacFilter] = useState<string>('')

  const params = new URLSearchParams()
  if (typeFilter) params.set('type', typeFilter)
  if (macFilter) params.set('mac', macFilter)
  params.set('limit', '500')
  const qs = params.toString()

  const { data, isLoading, refetch } = useQuery<BLEDetection[]>({
    queryKey: ['ble-detections', typeFilter, macFilter],
    queryFn: () => api.get<BLEDetection[]>(`/ble/detections?${qs}`),
    refetchInterval: 5000,
  })

  const rows = data ?? []
  const trackerCount = rows.filter((r) => {
    const lower = r.detection_type?.toLowerCase() ?? ''
    return ['airtag', 'tile', 'samsung_smarttag', 'chipolo', 'pebblebee', 'eufy', 'nut', 'macless_haystack'].includes(lower)
  }).length
  const surveillanceCount = rows.filter((r) => r.detection_type?.toLowerCase().startsWith('surveillance_')).length

  return (
    <div className="p-4">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-dark-100">BLE Detections</h1>
          <p className="text-sm text-dark-400">
            {rows.length} rows ・ {trackerCount} trackers ・ {surveillanceCount} surveillance
          </p>
        </div>
        <button
          onClick={() => refetch()}
          className="px-3 py-1.5 text-sm rounded bg-dark-700 hover:bg-dark-600 text-dark-100 border border-dark-600"
        >
          Refresh
        </button>
      </div>

      <div className="mb-4 flex gap-3">
        <input
          type="text"
          placeholder="Filter by detection type (e.g. airtag, tile)"
          value={typeFilter}
          onChange={(e) => setTypeFilter(e.target.value)}
          className="flex-1 px-3 py-1.5 text-sm rounded bg-dark-800 border border-dark-600 text-dark-100"
        />
        <input
          type="text"
          placeholder="Filter by MAC (AA:BB:CC:DD:EE:FF)"
          value={macFilter}
          onChange={(e) => setMacFilter(e.target.value.toUpperCase())}
          className="flex-1 px-3 py-1.5 text-sm rounded bg-dark-800 border border-dark-600 text-dark-100"
        />
      </div>

      {isLoading ? (
        <p className="text-dark-400">Loading…</p>
      ) : rows.length === 0 ? (
        <p className="text-dark-400">
          No BLE detections yet. Enable raw mode on a sensor with{' '}
          <code className="bg-dark-700 px-1 rounded">@HBxx RAW_BLE_ON</code> from CommandsPage and run a device scan.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm border-collapse">
            <thead>
              <tr className="text-left text-dark-400 border-b border-dark-700">
                <th className="px-2 py-2">Type</th>
                <th className="px-2 py-2">MAC</th>
                <th className="px-2 py-2">Manufacturer</th>
                <th className="px-2 py-2">Local Name</th>
                <th className="px-2 py-2">RSSI</th>
                <th className="px-2 py-2">CH</th>
                <th className="px-2 py-2">FindMy</th>
                <th className="px-2 py-2">Node</th>
                <th className="px-2 py-2">Seen</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => {
                const badge = detectionBadge(r.detection_type)
                return (
                  <tr key={r.id} className="border-b border-dark-800 hover:bg-dark-800/40">
                    <td className="px-2 py-1.5">
                      <span className={`inline-block px-2 py-0.5 text-xs rounded border ${badge.color}`}>{badge.label}</span>
                    </td>
                    <td className="px-2 py-1.5 font-mono text-dark-200">
                      {r.mac}
                      {r.is_random_addr && <span className="ml-1 text-xs text-dark-500">(rnd)</span>}
                    </td>
                    <td className="px-2 py-1.5 text-dark-300">{r.manufacturer || '-'}</td>
                    <td className="px-2 py-1.5 text-dark-300">{r.local_name || '-'}</td>
                    <td className="px-2 py-1.5">
                      <span className={`px-1.5 py-0.5 text-xs rounded ${rssiColor(r.rssi)}`}>{r.rssi} dBm</span>
                    </td>
                    <td className="px-2 py-1.5 text-dark-400">{r.channel || '-'}</td>
                    <td className="px-2 py-1.5 text-dark-400">{r.findmy_score ?? '-'}</td>
                    <td className="px-2 py-1.5 font-mono text-dark-300">{r.node_id}</td>
                    <td className="px-2 py-1.5 text-dark-400">{timeAgo(r.timestamp)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
