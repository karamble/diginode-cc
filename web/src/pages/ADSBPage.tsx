import { useQuery } from '@tanstack/react-query'
import api from '../api/client'

interface Aircraft {
  hex: string
  flight?: string
  alt_baro?: number
  alt_geom?: number
  gs?: number
  track?: number
  baro_rate?: number
  squawk?: string
  category?: string
  lat?: number
  lon?: number
  rssi?: number
  emergency?: string
  nav_altitude?: number
  nav_heading?: number
  seen?: number
  messages?: number
}

interface ADSBStatus {
  enabled: boolean
  url: string
  trackCount: number
  status: string
}

export default function ADSBPage() {
  const { data: status } = useQuery<ADSBStatus>({
    queryKey: ['adsb-status'],
    queryFn: () => api.get('/adsb/status'),
    refetchInterval: 5000,
  })

  const { data: aircraft, isLoading, error } = useQuery<Aircraft[]>({
    queryKey: ['adsb-tracks'],
    queryFn: () => api.get('/adsb/tracks'),
    refetchInterval: 3000,
  })

  const formatAlt = (alt?: number) => {
    if (alt === undefined || alt === null) return '-'
    return alt.toLocaleString() + ' ft'
  }

  const formatSpeed = (gs?: number) => {
    if (gs === undefined || gs === null) return '-'
    return Math.round(gs) + ' kts'
  }

  const formatTrack = (track?: number) => {
    if (track === undefined || track === null) return '-'
    return Math.round(track) + '\u00B0'
  }

  const formatCoord = (val?: number) => {
    if (val === undefined || val === null || val === 0) return '-'
    return val.toFixed(4)
  }

  const squawkColor = (squawk?: string) => {
    if (!squawk) return ''
    if (squawk === '7500') return 'text-red-400' // hijack
    if (squawk === '7600') return 'text-orange-400' // radio failure
    if (squawk === '7700') return 'text-red-500 font-bold' // emergency
    return ''
  }

  // Sort by messages count descending (most active first)
  const sorted = aircraft ? [...aircraft].sort((a, b) => (b.messages || 0) - (a.messages || 0)) : []

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">ADS-B Tracker</h2>
          <p className="text-sm text-dark-400 mt-1">
            {sorted.length} aircraft tracked
          </p>
        </div>

        {/* Status indicator */}
        <div className="flex items-center gap-3">
          {status && (
            <div className="flex items-center gap-2 px-3 py-1.5 bg-surface rounded-lg border border-dark-700/50">
              <span className={`inline-flex w-2 h-2 rounded-full ${status.enabled ? 'bg-green-400 animate-pulse' : 'bg-dark-600'}`} />
              <span className="text-xs text-dark-300">
                {status.enabled ? status.status : 'Disabled'}
              </span>
              {status.enabled && (
                <span className="text-xs text-dark-500">
                  {status.trackCount} tracks
                </span>
              )}
            </div>
          )}
        </div>
      </div>

      <div className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
        {isLoading ? (
          <div className="p-8 text-center">
            <div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
            <p className="mt-2 text-sm text-dark-400">Loading aircraft tracks...</p>
          </div>
        ) : error ? (
          <div className="p-8 text-center">
            <p className="text-sm text-red-400">Failed to load ADS-B data: {(error as Error).message}</p>
          </div>
        ) : sorted.length === 0 ? (
          <div className="p-8 text-center">
            <p className="text-sm text-dark-400">
              {status?.enabled ? 'No aircraft currently tracked' : 'ADS-B receiver not enabled'}
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Hex</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Flight</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Altitude</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Speed</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Track</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Squawk</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Lat</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Lon</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">RSSI</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Msgs</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {sorted.map((ac) => (
                  <tr
                    key={ac.hex}
                    className={`hover:bg-dark-800/30 transition-colors ${ac.emergency && ac.emergency !== 'none' ? 'bg-red-900/10' : ''}`}
                  >
                    <td className="px-4 py-3 text-sm text-dark-200 font-mono uppercase">{ac.hex}</td>
                    <td className="px-4 py-3">
                      <span className="text-sm text-dark-200 font-medium">
                        {ac.flight?.trim() || '-'}
                      </span>
                      {ac.category && (
                        <span className="ml-2 text-xs text-dark-500">{ac.category}</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-300 text-right font-mono">{formatAlt(ac.alt_baro)}</td>
                    <td className="px-4 py-3 text-sm text-dark-300 text-right font-mono">{formatSpeed(ac.gs)}</td>
                    <td className="px-4 py-3 text-sm text-dark-300 text-right font-mono">{formatTrack(ac.track)}</td>
                    <td className="px-4 py-3">
                      {ac.squawk ? (
                        <span className={`text-sm font-mono ${squawkColor(ac.squawk)}`}>{ac.squawk}</span>
                      ) : (
                        <span className="text-xs text-dark-500">-</span>
                      )}
                      {ac.emergency && ac.emergency !== 'none' && (
                        <span className="ml-2 text-xs text-red-400 font-medium uppercase">{ac.emergency}</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-400 text-right font-mono">{formatCoord(ac.lat)}</td>
                    <td className="px-4 py-3 text-sm text-dark-400 text-right font-mono">{formatCoord(ac.lon)}</td>
                    <td className="px-4 py-3 text-sm text-dark-400 text-right font-mono">
                      {ac.rssi !== undefined ? ac.rssi.toFixed(1) : '-'}
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-400 text-right font-mono">{ac.messages || 0}</td>
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
