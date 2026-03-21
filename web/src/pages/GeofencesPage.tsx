import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
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

interface GeofenceForm {
  name: string
  description: string
  color: string
  alarmEnabled: boolean
  alarmLevel: string
  alarmMessage: string
  triggerOnEntry: boolean
  triggerOnExit: boolean
  appliesToAdsb: boolean
  appliesToDrones: boolean
  appliesToTargets: boolean
  appliesToDevices: boolean
  polygonJson: string
}

const PRESET_COLORS = [
  '#EF4444', '#F97316', '#EAB308', '#22C55E',
  '#3B82F6', '#8B5CF6', '#EC4899', '#06B6D4',
]

const ALARM_LEVELS = ['INFO', 'NOTICE', 'ALERT', 'CRITICAL']

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

const emptyForm: GeofenceForm = {
  name: '',
  description: '',
  color: '#3B82F6',
  alarmEnabled: false,
  alarmLevel: 'ALERT',
  alarmMessage: '{entity} entered {geofence}',
  triggerOnEntry: true,
  triggerOnExit: false,
  appliesToAdsb: false,
  appliesToDrones: true,
  appliesToTargets: false,
  appliesToDevices: false,
  polygonJson: '',
}

function formToPayload(form: GeofenceForm) {
  let polygon: Point[] = []
  try {
    polygon = JSON.parse(form.polygonJson)
  } catch {
    // will be validated before submit
  }
  return {
    name: form.name,
    description: form.description || undefined,
    color: form.color,
    polygon,
    action: form.alarmEnabled ? 'ALARM' : 'LOG',
    enabled: true,
    alarmEnabled: form.alarmEnabled,
    alarmLevel: form.alarmEnabled ? form.alarmLevel : undefined,
    alarmMessage: form.alarmEnabled ? form.alarmMessage : undefined,
    triggerOnEntry: form.triggerOnEntry,
    triggerOnExit: form.triggerOnExit,
    appliesToAdsb: form.appliesToAdsb,
    appliesToDrones: form.appliesToDrones,
    appliesToTargets: form.appliesToTargets,
    appliesToDevices: form.appliesToDevices,
  }
}

function geofenceToForm(g: Geofence): GeofenceForm {
  return {
    name: g.name,
    description: g.description || '',
    color: g.color || '#3B82F6',
    alarmEnabled: g.alarmEnabled,
    alarmLevel: g.alarmLevel || 'ALERT',
    alarmMessage: g.alarmMessage || '{entity} entered {geofence}',
    triggerOnEntry: g.triggerOnEntry,
    triggerOnExit: g.triggerOnExit,
    appliesToAdsb: g.appliesToAdsb,
    appliesToDrones: g.appliesToDrones,
    appliesToTargets: g.appliesToTargets,
    appliesToDevices: g.appliesToDevices,
    polygonJson: JSON.stringify(g.polygon || [], null, 2),
  }
}

function validateForm(form: GeofenceForm): string | null {
  if (!form.name.trim()) return 'Name is required'
  if (!form.triggerOnEntry && !form.triggerOnExit) return 'At least one trigger (Entry or Exit) must be selected'
  try {
    const pts = JSON.parse(form.polygonJson)
    if (!Array.isArray(pts) || pts.length < 3) return 'Polygon must have at least 3 points'
    for (const p of pts) {
      if (typeof p.lat !== 'number' || typeof p.lng !== 'number') return 'Each polygon point must have "lat" and "lng" numbers'
    }
  } catch {
    return 'Polygon JSON is invalid'
  }
  return null
}

export default function GeofencesPage() {
  const queryClient = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<GeofenceForm>(emptyForm)
  const [formError, setFormError] = useState<string | null>(null)

  const { data: geofences, isLoading, error } = useQuery<Geofence[]>({
    queryKey: ['geofences'],
    queryFn: () => api.get('/geofences'),
  })

  const createMutation = useMutation({
    mutationFn: (body: ReturnType<typeof formToPayload>) => api.post('/geofences', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['geofences'] })
      setShowForm(false)
      setForm(emptyForm)
      setFormError(null)
    },
    onError: (err: Error) => setFormError(err.message),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: ReturnType<typeof formToPayload> }) =>
      api.put(`/geofences/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['geofences'] })
      setShowForm(false)
      setEditingId(null)
      setForm(emptyForm)
      setFormError(null)
    },
    onError: (err: Error) => setFormError(err.message),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/geofences/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['geofences'] }),
  })

  const handleSubmit = () => {
    const validationError = validateForm(form)
    if (validationError) {
      setFormError(validationError)
      return
    }
    setFormError(null)
    const payload = formToPayload(form)
    if (editingId) {
      updateMutation.mutate({ id: editingId, body: payload })
    } else {
      createMutation.mutate(payload)
    }
  }

  const handleEdit = (g: Geofence) => {
    setEditingId(g.id)
    setForm(geofenceToForm(g))
    setFormError(null)
    setShowForm(true)
  }

  const handleCancel = () => {
    setShowForm(false)
    setEditingId(null)
    setForm(emptyForm)
    setFormError(null)
  }

  const handleNew = () => {
    setEditingId(null)
    setForm(emptyForm)
    setFormError(null)
    setShowForm(true)
  }

  const updateField = <K extends keyof GeofenceForm>(key: K, value: GeofenceForm[K]) => {
    setForm(prev => ({ ...prev, [key]: value }))
  }

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

  const isPending = createMutation.isPending || updateMutation.isPending

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-lg font-semibold text-dark-100">Geofences</h2>
        <div className="flex items-center gap-3">
          <span className="text-sm text-dark-400">
            {geofences ? `${geofences.length} geofence${geofences.length !== 1 ? 's' : ''}` : ''}
          </span>
          {!showForm && (
            <button
              onClick={handleNew}
              className="px-4 py-2 bg-primary-600 hover:bg-primary-700 text-white text-sm rounded font-medium transition-colors"
            >
              New Geofence
            </button>
          )}
        </div>
      </div>

      {/* Create / Edit Form */}
      {showForm && (
        <div className="bg-dark-900 rounded-lg border border-dark-700 p-5 mb-6">
          <h3 className="text-sm font-semibold text-dark-100 mb-4">
            {editingId ? 'Edit Geofence' : 'New Geofence'}
          </h3>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            {/* Name */}
            <div>
              <label className="block text-xs text-dark-400 mb-1">Name</label>
              <input
                type="text"
                value={form.name}
                onChange={e => updateField('name', e.target.value)}
                placeholder="Geofence name"
                className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
              />
            </div>

            {/* Description */}
            <div>
              <label className="block text-xs text-dark-400 mb-1">Description</label>
              <textarea
                value={form.description}
                onChange={e => updateField('description', e.target.value)}
                placeholder="Optional description"
                rows={1}
                className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500 resize-none"
              />
            </div>

            {/* Color picker */}
            <div>
              <label className="block text-xs text-dark-400 mb-1">Color</label>
              <div className="flex items-center gap-2">
                {PRESET_COLORS.map(c => (
                  <button
                    key={c}
                    onClick={() => updateField('color', c)}
                    className="w-7 h-7 rounded border-2 transition-all"
                    style={{
                      backgroundColor: c,
                      borderColor: form.color === c ? '#fff' : 'transparent',
                      transform: form.color === c ? 'scale(1.15)' : 'scale(1)',
                    }}
                  />
                ))}
                <div
                  className="w-7 h-7 rounded border border-dark-600 ml-1"
                  style={{ backgroundColor: form.color }}
                />
              </div>
            </div>

            {/* Alarm enabled + level */}
            <div>
              <label className="block text-xs text-dark-400 mb-1">Alarm</label>
              <div className="flex items-center gap-3">
                <label className="flex items-center gap-1.5 text-sm text-dark-300 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.alarmEnabled}
                    onChange={e => updateField('alarmEnabled', e.target.checked)}
                    className="rounded border-dark-600 bg-dark-800 text-primary-500 focus:ring-primary-500"
                  />
                  Enabled
                </label>
                {form.alarmEnabled && (
                  <select
                    value={form.alarmLevel}
                    onChange={e => updateField('alarmLevel', e.target.value)}
                    className="px-2 py-1 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
                  >
                    {ALARM_LEVELS.map(lv => (
                      <option key={lv} value={lv}>{lv}</option>
                    ))}
                  </select>
                )}
              </div>
            </div>

            {/* Alarm message */}
            {form.alarmEnabled && (
              <div className="lg:col-span-2">
                <label className="block text-xs text-dark-400 mb-1">
                  Alarm Message <span className="text-dark-500">(use {'{entity}'} and {'{geofence}'} placeholders)</span>
                </label>
                <input
                  type="text"
                  value={form.alarmMessage}
                  onChange={e => updateField('alarmMessage', e.target.value)}
                  placeholder="{entity} entered {geofence}"
                  className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
                />
              </div>
            )}

            {/* Triggers */}
            <div>
              <label className="block text-xs text-dark-400 mb-1">Trigger</label>
              <div className="flex items-center gap-4">
                <label className="flex items-center gap-1.5 text-sm text-dark-300 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.triggerOnEntry}
                    onChange={e => updateField('triggerOnEntry', e.target.checked)}
                    className="rounded border-dark-600 bg-dark-800 text-primary-500 focus:ring-primary-500"
                  />
                  Entry
                </label>
                <label className="flex items-center gap-1.5 text-sm text-dark-300 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.triggerOnExit}
                    onChange={e => updateField('triggerOnExit', e.target.checked)}
                    className="rounded border-dark-600 bg-dark-800 text-primary-500 focus:ring-primary-500"
                  />
                  Exit
                </label>
              </div>
            </div>

            {/* Entity filters */}
            <div>
              <label className="block text-xs text-dark-400 mb-1">Entity Filters</label>
              <div className="flex items-center gap-4">
                <label className="flex items-center gap-1.5 text-sm text-dark-300 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.appliesToAdsb}
                    onChange={e => updateField('appliesToAdsb', e.target.checked)}
                    className="rounded border-dark-600 bg-dark-800 text-primary-500 focus:ring-primary-500"
                  />
                  ADS-B
                </label>
                <label className="flex items-center gap-1.5 text-sm text-dark-300 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.appliesToDrones}
                    onChange={e => updateField('appliesToDrones', e.target.checked)}
                    className="rounded border-dark-600 bg-dark-800 text-primary-500 focus:ring-primary-500"
                  />
                  Drones
                </label>
                <label className="flex items-center gap-1.5 text-sm text-dark-300 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.appliesToTargets}
                    onChange={e => updateField('appliesToTargets', e.target.checked)}
                    className="rounded border-dark-600 bg-dark-800 text-primary-500 focus:ring-primary-500"
                  />
                  Targets
                </label>
                <label className="flex items-center gap-1.5 text-sm text-dark-300 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.appliesToDevices}
                    onChange={e => updateField('appliesToDevices', e.target.checked)}
                    className="rounded border-dark-600 bg-dark-800 text-primary-500 focus:ring-primary-500"
                  />
                  Devices
                </label>
              </div>
            </div>

            {/* Polygon JSON */}
            <div className="lg:col-span-2">
              <label className="block text-xs text-dark-400 mb-1">
                Polygon <span className="text-dark-500">(JSON array of {'{lat, lng}'} points, min 3)</span>
              </label>
              <textarea
                value={form.polygonJson}
                onChange={e => updateField('polygonJson', e.target.value)}
                placeholder={'[\n  {"lat": 47.376, "lng": 8.541},\n  {"lat": 47.377, "lng": 8.543},\n  {"lat": 47.375, "lng": 8.543}\n]'}
                rows={5}
                className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm font-mono focus:outline-none focus:border-primary-500 resize-y"
              />
            </div>
          </div>

          {/* Form error */}
          {formError && (
            <p className="mt-3 text-sm text-red-400">{formError}</p>
          )}

          {/* Form actions */}
          <div className="flex items-center gap-3 mt-4">
            <button
              onClick={handleSubmit}
              disabled={isPending}
              className="px-4 py-2 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors"
            >
              {isPending ? 'Saving...' : editingId ? 'Update' : 'Create'}
            </button>
            <button
              onClick={handleCancel}
              className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-300 text-sm rounded font-medium transition-colors"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

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
                    <td className="px-4 py-3 text-right space-x-3">
                      <button
                        onClick={() => handleEdit(g)}
                        className="text-xs text-primary-400 hover:text-primary-300 transition-colors"
                      >
                        Edit
                      </button>
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
