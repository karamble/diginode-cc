import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useMemo } from 'react'
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
  channel?: number
  locallyAdministered?: boolean
  multicast?: boolean
}

type SortKey = 'mac' | 'manufacturer' | 'lastSsid' | 'deviceType' | 'rssi' | 'hits' | 'channel' | 'lastNodeId' | 'lastSeen' | 'firstSeen' | 'avgRssi' | 'minRssi' | 'maxRssi' | 'lastLat'
type SortDir = 'asc' | 'desc'

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

function SortIcon({ active, dir }: { active: boolean; dir: SortDir }) {
  if (!active) return <span className="text-dark-600 ml-1">&#x2195;</span>
  return <span className="text-primary-400 ml-1">{dir === 'asc' ? '\u2191' : '\u2193'}</span>
}

export default function InventoryPage() {
  const queryClient = useQueryClient()
  const [promotedMAC, setPromotedMAC] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [sortKey, setSortKey] = useState<SortKey>('lastSeen')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

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
    onSuccess: (_data, mac) => {
      setPromotedMAC(mac)
      queryClient.invalidateQueries({ queryKey: ['inventory'] })
      queryClient.invalidateQueries({ queryKey: ['targets'] })
      setTimeout(() => setPromotedMAC(null), 3000)
    },
  })

  const handleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir(d => d === 'asc' ? 'desc' : 'asc')
    } else {
      setSortKey(key)
      setSortDir(key === 'lastSeen' || key === 'firstSeen' ? 'desc' : 'asc')
    }
  }

  const filtered = useMemo(() => {
    if (!devices) return []
    const q = search.toLowerCase().trim()
    if (!q) return devices
    return devices.filter(d =>
      d.mac.toLowerCase().includes(q) ||
      (d.manufacturer || '').toLowerCase().includes(q) ||
      (d.lastSsid || '').toLowerCase().includes(q) ||
      (d.deviceName || '').toLowerCase().includes(q) ||
      (d.deviceType || '').toLowerCase().includes(q) ||
      (d.lastNodeId || '').toLowerCase().includes(q)
    )
  }, [devices, search])

  const sorted = useMemo(() => {
    const list = [...filtered]
    const dir = sortDir === 'asc' ? 1 : -1

    list.sort((a, b) => {
      let cmp = 0
      switch (sortKey) {
        case 'mac': cmp = a.mac.localeCompare(b.mac); break
        case 'manufacturer': cmp = (a.manufacturer || '').localeCompare(b.manufacturer || ''); break
        case 'lastSsid': cmp = (a.lastSsid || a.deviceName || '').localeCompare(b.lastSsid || b.deviceName || ''); break
        case 'deviceType': cmp = (a.deviceType || '').localeCompare(b.deviceType || ''); break
        case 'rssi': cmp = (a.rssi || -999) - (b.rssi || -999); break
        case 'avgRssi': cmp = (a.avgRssi || -999) - (b.avgRssi || -999); break
        case 'minRssi': cmp = (a.minRssi || -999) - (b.minRssi || -999); break
        case 'maxRssi': cmp = (a.maxRssi || -999) - (b.maxRssi || -999); break
        case 'hits': cmp = a.hits - b.hits; break
        case 'channel': cmp = (a.channel || 0) - (b.channel || 0); break
        case 'lastNodeId': cmp = (a.lastNodeId || '').localeCompare(b.lastNodeId || ''); break
        case 'lastLat': cmp = (a.lastLat || 0) - (b.lastLat || 0); break
        case 'lastSeen': cmp = new Date(a.lastSeen).getTime() - new Date(b.lastSeen).getTime(); break
        case 'firstSeen': cmp = new Date(a.firstSeen).getTime() - new Date(b.firstSeen).getTime(); break
      }
      return cmp * dir
    })
    return list
  }, [filtered, sortKey, sortDir])

  const thClass = "text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3 cursor-pointer select-none hover:text-dark-200 transition-colors whitespace-nowrap"
  const thClassRight = "text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3 cursor-pointer select-none hover:text-dark-200 transition-colors whitespace-nowrap"

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">Device Inventory</h2>
          <p className="text-sm text-dark-400 mt-1">
            {sorted.length} device{sorted.length !== 1 ? 's' : ''} tracked
            {devices && sorted.length !== devices.length && (
              <span> (of {devices.length} total)</span>
            )}
            {sorted.filter(d => d.isKnown).length > 0 && (
              <span> &middot; {sorted.filter(d => d.isKnown).length} known</span>
            )}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <input
            type="text"
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="Search MAC, manufacturer, SSID..."
            className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-sm text-dark-200 placeholder-dark-500 focus:outline-none focus:border-primary-500 w-64"
          />
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
            <p className="text-sm text-dark-400">
              {search ? 'No devices match your search' : 'No devices in inventory'}
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className={thClass} onClick={() => handleSort('mac')}>
                    MAC<SortIcon active={sortKey === 'mac'} dir={sortDir} />
                  </th>
                  <th className={thClass} onClick={() => handleSort('manufacturer')}>
                    Manufacturer<SortIcon active={sortKey === 'manufacturer'} dir={sortDir} />
                  </th>
                  <th className={thClass} onClick={() => handleSort('lastSsid')}>
                    Name/SSID<SortIcon active={sortKey === 'lastSsid'} dir={sortDir} />
                  </th>
                  <th className={thClass} onClick={() => handleSort('deviceType')}>
                    Type<SortIcon active={sortKey === 'deviceType'} dir={sortDir} />
                  </th>
                  <th className={thClass} onClick={() => handleSort('channel')}>
                    Ch<SortIcon active={sortKey === 'channel'} dir={sortDir} />
                  </th>
                  <th className={thClass} onClick={() => handleSort('rssi')}>
                    RSSI<SortIcon active={sortKey === 'rssi'} dir={sortDir} />
                  </th>
                  <th className={thClassRight} onClick={() => handleSort('hits')}>
                    Hits<SortIcon active={sortKey === 'hits'} dir={sortDir} />
                  </th>
                  <th className={thClass} onClick={() => handleSort('lastLat')}>
                    Location<SortIcon active={sortKey === 'lastLat'} dir={sortDir} />
                  </th>
                  <th className={thClass} onClick={() => handleSort('lastNodeId')}>
                    Node<SortIcon active={sortKey === 'lastNodeId'} dir={sortDir} />
                  </th>
                  <th className={thClass} onClick={() => handleSort('lastSeen')}>
                    Last Seen<SortIcon active={sortKey === 'lastSeen'} dir={sortDir} />
                  </th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {sorted.map((dev) => (
                  <tr key={dev.mac} className="hover:bg-dark-800/30 transition-colors">
                    <td className="px-4 py-3">
                      <span className="text-sm text-dark-200 font-mono">{dev.mac}</span>
                      <div className="flex gap-1 mt-0.5">
                        {dev.locallyAdministered && (
                          <span className="text-[10px] px-1 py-0.5 rounded bg-yellow-500/20 text-yellow-400" title="Locally administered (randomized)">LA</span>
                        )}
                        {dev.multicast && (
                          <span className="text-[10px] px-1 py-0.5 rounded bg-purple-500/20 text-purple-400" title="Multicast address">MC</span>
                        )}
                        {dev.deviceName && (
                          <span className="text-xs text-dark-500">{dev.deviceName}</span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-300">{dev.manufacturer || '-'}</td>
                    <td className="px-4 py-3 text-sm text-dark-300 truncate max-w-[120px]">{dev.lastSsid || dev.deviceName || '-'}</td>
                    <td className="px-4 py-3 text-sm text-dark-300">{dev.deviceType || '-'}</td>
                    <td className="px-4 py-3 text-sm text-dark-400 font-mono">{dev.channel || '-'}</td>
                    <td className="px-4 py-3">
                      <RSSIBar rssi={dev.rssi} />
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-300 text-right font-mono">{dev.hits}</td>
                    <td className="px-4 py-3 text-sm text-dark-400 font-mono">
                      {dev.lastLat && dev.lastLon && dev.lastLat !== 0
                        ? `${dev.lastLat.toFixed(4)}, ${dev.lastLon.toFixed(4)}`
                        : '-'}
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-400 font-mono">{dev.lastNodeId || '-'}</td>
                    <td className="px-4 py-3 text-sm text-dark-400">{formatDate(dev.lastSeen)}</td>
                    <td className="px-4 py-3 text-right">
                      {promotedMAC === dev.mac ? (
                        <span className="text-xs text-green-400 font-medium">Promoted!</span>
                      ) : (
                        <button
                          onClick={() => promoteMutation.mutate(dev.mac)}
                          disabled={promoteMutation.isPending}
                          className="text-xs text-primary-400 hover:text-primary-300 transition-colors"
                          title="Promote to target"
                        >
                          {promoteMutation.isPending && promoteMutation.variables === dev.mac ? 'Promoting...' : 'Promote'}
                        </button>
                      )}
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
