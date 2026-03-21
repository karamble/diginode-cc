import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from '../api/client'

interface Point {
  lat: number
  lng: number
}

interface Geofence {
  id: string
  name: string
  description?: string
  color?: string
  polygon: Point[]
  action: string
  enabled: boolean
  alarmEnabled: boolean
  alarmLevel?: string
  alarmMessage?: string
  triggerOnEntry: boolean
  triggerOnExit: boolean
  appliesToAdsb: boolean
  appliesToDrones: boolean
  appliesToTargets: boolean
  appliesToDevices: boolean
  siteId?: string
  originSiteId?: string
}

const actionBadge: Record<string, string> = {
  ALERT: 'bg-orange-600/20 text-orange-400 border-orange-500/30',
  LOG: 'bg-blue-600/20 text-blue-400 border-blue-500/30',
  ALARM: 'bg-red-600/20 text-red-400 border-red-500/30',
}

const alarmLevelColors: Record<string, string> = {
  INFO: 'text-sky-400',
  NOTICE: 'text-green-400',
  ALERT: 'text-orange-400',
  CRITICAL: 'text-red-400',
}

export default function GeofencesPage() {
  const queryClient = useQueryClient()

  const { data: geofences, isLoading, error } = useQuery<Geofence[]>({
    queryKey: ['geofences'],
    queryFn: () => api.get('/geofences'),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/geofences/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['geofences'] }),
  })

  const entityFilters = (g: Geofence) => {
    const filters: string[] = []
    if (g.appliesToAdsb) filters.push('ADS-B')
    if (g.appliesToDrones) filters.push('Drones')
    if (g.appliesToTargets) filters.push('Targets')
    if (g.appliesToDevices) filters.push('Devices')
    return filters.length > 0 ? filters.join(', ') : 'None'
  }

  const triggerDesc = (g: Geofence) => {
    const parts: string[] = []
    if (g.triggerOnEntry) parts.push('Entry')
    if (g.triggerOnExit) parts.push('Exit')
    return parts.join(' / ') || '-'
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-lg font-semibold text-dark-100">Geofences</h2>
        <span className="text-sm text-dark-400">
          {geofences ? `${geofences.length} geofence${geofences.length !== 1 ? 's' : ''}` : ''}
        </span>
      </div>

      <div className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
        {isLoading ? (
          <div className="p-8 text-center">
            <div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
            <p className="mt-2 text-sm text-dark-400">Loading geofences...</p>
          </div>
        ) : error ? (
          <div className="p-8 text-center">
            <p className="text-sm text-red-400">Failed to load geofences: {(error as Error).message}</p>
          </div>
        ) : !geofences || geofences.length === 0 ? (
          <div className="p-8 text-center">
            <p className="text-sm text-dark-400">No geofences defined</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Name</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Color</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Action</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Enabled</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Alarm Level</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Trigger</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Points</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Entities</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {geofences.map((g: Geofence) => (
                  <tr key={g.id} className="hover:bg-dark-800/30 transition-colors">
                    <td className="px-4 py-3">
                      <div className="text-sm text-dark-200 font-medium">{g.name}</div>
                      {g.description && (
                        <div className="text-xs text-dark-500 mt-0.5 truncate max-w-[200px]">{g.description}</div>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      {g.color ? (
                        <div className="flex items-center gap-2">
                          <div
                            className="w-4 h-4 rounded-sm border border-dark-600"
                            style={{ backgroundColor: g.color }}
                          />
                          <span className="text-xs text-dark-400 font-mono">{g.color}</span>
                        </div>
                      ) : (
                        <span className="text-xs text-dark-500">-</span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <span className={`inline-flex px-2 py-0.5 text-xs font-medium rounded border ${actionBadge[g.action] || 'bg-dark-700/50 text-dark-400 border-dark-600'}`}>
                        {g.action}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      {g.enabled ? (
                        <span className="inline-flex w-2 h-2 rounded-full bg-green-400" />
                      ) : (
                        <span className="inline-flex w-2 h-2 rounded-full bg-dark-600" />
                      )}
                    </td>
                    <td className="px-4 py-3">
                      {g.alarmLevel ? (
                        <span className={`text-xs font-medium ${alarmLevelColors[g.alarmLevel] || 'text-dark-400'}`}>
                          {g.alarmLevel}
                        </span>
                      ) : (
                        <span className="text-xs text-dark-500">-</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs text-dark-300">{triggerDesc(g)}</td>
                    <td className="px-4 py-3">
                      <span className="text-xs text-dark-300 font-mono">{g.polygon?.length || 0}</span>
                    </td>
                    <td className="px-4 py-3 text-xs text-dark-300 max-w-[160px] truncate">{entityFilters(g)}</td>
                    <td className="px-4 py-3 text-right">
                      <button
                        onClick={() => {
                          if (confirm(`Delete geofence "${g.name}"?`)) {
                            deleteMutation.mutate(g.id)
                          }
                        }}
                        className="text-xs text-red-400 hover:text-red-300 transition-colors"
                      >
                        Delete
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
