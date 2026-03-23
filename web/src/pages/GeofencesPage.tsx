import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useCallback } from 'react'
import { MapContainer, TileLayer, Polygon, Polyline, CircleMarker, Tooltip, useMapEvents, useMap } from 'react-leaflet'
import 'leaflet/dist/leaflet.css'
import api from '../api/client'
import { useNodesStore } from '../stores/nodesStore'

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
  notifyWebhook: boolean
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
  notifyWebhook: boolean
}

const PRESET_COLORS = [
  '#EF4444', '#F97316', '#EAB308', '#22C55E',
  '#3B82F6', '#8B5CF6', '#EC4899', '#06B6D4',
]

const ALARM_LEVELS = ['INFO', 'NOTICE', 'ALERT', 'CRITICAL']

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
  notifyWebhook: false,
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
    notifyWebhook: g.notifyWebhook ?? false,
  }
}

// --- Drawing handler: captures map clicks and mouse moves ---
function DrawingHandler({ enabled, onPoint, onHover }: {
  enabled: boolean
  onPoint: (p: Point) => void
  onHover: (p: Point | null) => void
}) {
  useMapEvents({
    click(e) {
      if (enabled) onPoint({ lat: e.latlng.lat, lng: e.latlng.lng })
    },
    mousemove(e) {
      if (enabled) onHover({ lat: e.latlng.lat, lng: e.latlng.lng })
    },
    mouseout() {
      if (enabled) onHover(null)
    },
  })
  return null
}

// --- Auto-fit map to geofence being edited ---
function FitToPoints({ points }: { points: Point[] }) {
  const map = useMap()
  if (points.length > 0) {
    const bounds = points.map(p => [p.lat, p.lng] as [number, number])
    try { map.fitBounds(bounds, { padding: [40, 40], maxZoom: 16 }) } catch { /* ignore */ }
  }
  return null
}

export default function GeofencesPage() {
  const queryClient = useQueryClient()

  // Form state
  const [showForm, setShowForm] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<GeofenceForm>(emptyForm)
  const [formError, setFormError] = useState<string | null>(null)

  // Drawing state
  const [draftVertices, setDraftVertices] = useState<Point[]>([])
  const [hoverPoint, setHoverPoint] = useState<Point | null>(null)
  const [fitOnce, setFitOnce] = useState(false)

  const { data: geofences = [], isLoading } = useQuery<Geofence[]>({
    queryKey: ['geofences'],
    queryFn: () => api.get('/geofences'),
  })

  const createMutation = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.post('/geofences', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['geofences'] })
      handleCancel()
    },
    onError: (err: Error) => setFormError(err.message),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Record<string, unknown> }) =>
      api.put(`/geofences/${id}`, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['geofences'] })
      handleCancel()
    },
    onError: (err: Error) => setFormError(err.message),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/geofences/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['geofences'] }),
  })

  const handleSubmit = () => {
    if (!form.name.trim()) { setFormError('Name is required'); return }
    if (!form.triggerOnEntry && !form.triggerOnExit) { setFormError('Select at least one trigger'); return }
    if (draftVertices.length < 3) { setFormError('Draw at least 3 points on the map'); return }
    setFormError(null)

    const payload = {
      name: form.name,
      description: form.description || undefined,
      color: form.color,
      polygon: draftVertices,
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
      notifyWebhook: form.notifyWebhook,
    }

    if (editingId) {
      updateMutation.mutate({ id: editingId, body: payload })
    } else {
      createMutation.mutate(payload)
    }
  }

  const handleEdit = (g: Geofence) => {
    setEditingId(g.id)
    setForm(geofenceToForm(g))
    setDraftVertices(g.polygon || [])
    setFormError(null)
    setShowForm(true)
    setFitOnce(true)
  }

  const handleCancel = () => {
    setShowForm(false)
    setEditingId(null)
    setForm(emptyForm)
    setDraftVertices([])
    setHoverPoint(null)
    setFormError(null)
    setFitOnce(false)
  }

  const handleNew = () => {
    setEditingId(null)
    setForm(emptyForm)
    setDraftVertices([])
    setFormError(null)
    setShowForm(true)
    setFitOnce(false)
  }

  const handleMapClick = useCallback((p: Point) => {
    setDraftVertices(prev => [...prev, p])
  }, [])

  const handleUndo = () => {
    setDraftVertices(prev => prev.slice(0, -1))
  }

  const handleClearDraft = () => {
    setDraftVertices([])
  }

  const updateField = <K extends keyof GeofenceForm>(key: K, value: GeofenceForm[K]) => {
    setForm(prev => ({ ...prev, [key]: value }))
  }

  const isPending = createMutation.isPending || updateMutation.isPending

  // Build draft polyline positions (with hover preview)
  const draftPositions: [number, number][] = draftVertices.map(p => [p.lat, p.lng])
  if (hoverPoint && showForm) {
    draftPositions.push([hoverPoint.lat, hoverPoint.lng])
  }
  // Close the polygon preview
  const closedPositions = draftPositions.length >= 3
    ? [...draftPositions, draftPositions[0]]
    : draftPositions

  // Mesh nodes with GPS for map markers and auto-centering
  const allNodes = useNodesStore((s) => s.nodes)
  const nodesWithGPS = Array.from(allNodes.values()).filter(
    (n) => n.latitude && n.longitude && n.latitude !== 0 && n.longitude !== 0
  )

  // Center: first geofence polygon > first node with GPS > Zurich fallback
  const defaultCenter: [number, number] = geofences.length > 0 && geofences[0].polygon?.length > 0
    ? [geofences[0].polygon[0].lat, geofences[0].polygon[0].lng]
    : nodesWithGPS.length > 0
      ? [nodesWithGPS[0].latitude!, nodesWithGPS[0].longitude!]
      : [47.3769, 8.5417]

  return (
    <div className="flex h-full">
      {/* Left panel — list + form */}
      <div className="w-[420px] flex-shrink-0 flex flex-col border-r border-dark-700/50 overflow-y-auto">
        {/* Header */}
        <div className="p-4 border-b border-dark-700/50 flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold text-dark-100">Geofences</h2>
            <span className="text-xs text-dark-400">{geofences.length} defined</span>
          </div>
          {!showForm && (
            <button
              onClick={handleNew}
              className="px-3 py-1.5 bg-primary-600 hover:bg-primary-700 text-white text-xs rounded font-medium transition-colors"
            >
              New Geofence
            </button>
          )}
        </div>

        {/* Form */}
        {showForm && (
          <div className="p-4 border-b border-dark-700/50 bg-dark-900/50">
            <h3 className="text-sm font-semibold text-dark-100 mb-3">
              {editingId ? 'Edit Geofence' : 'New Geofence'}
            </h3>

            <div className="space-y-3">
              {/* Name */}
              <div>
                <label className="block text-[10px] text-dark-400 uppercase tracking-wider mb-1">Name</label>
                <input
                  type="text"
                  value={form.name}
                  onChange={e => updateField('name', e.target.value)}
                  placeholder="Geofence name"
                  className="w-full px-2.5 py-1.5 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
                />
              </div>

              {/* Description */}
              <div>
                <label className="block text-[10px] text-dark-400 uppercase tracking-wider mb-1">Description</label>
                <input
                  type="text"
                  value={form.description}
                  onChange={e => updateField('description', e.target.value)}
                  placeholder="Optional"
                  className="w-full px-2.5 py-1.5 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
                />
              </div>

              {/* Color */}
              <div>
                <label className="block text-[10px] text-dark-400 uppercase tracking-wider mb-1">Color</label>
                <div className="flex items-center gap-1.5">
                  {PRESET_COLORS.map(c => (
                    <button
                      key={c}
                      onClick={() => updateField('color', c)}
                      className="w-6 h-6 rounded border-2 transition-all"
                      style={{
                        backgroundColor: c,
                        borderColor: form.color === c ? '#fff' : 'transparent',
                        transform: form.color === c ? 'scale(1.15)' : 'scale(1)',
                      }}
                    />
                  ))}
                </div>
              </div>

              {/* Alarm */}
              <div className="flex items-center gap-3">
                <label className="flex items-center gap-1.5 text-xs text-dark-300 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.alarmEnabled}
                    onChange={e => updateField('alarmEnabled', e.target.checked)}
                    className="rounded border-dark-600 bg-dark-800 text-primary-500"
                  />
                  Alarm
                </label>
                {form.alarmEnabled && (
                  <select
                    value={form.alarmLevel}
                    onChange={e => updateField('alarmLevel', e.target.value)}
                    className="px-2 py-1 bg-dark-800 border border-dark-600 rounded text-dark-100 text-xs focus:outline-none"
                  >
                    {ALARM_LEVELS.map(lv => <option key={lv} value={lv}>{lv}</option>)}
                  </select>
                )}
              </div>

              {form.alarmEnabled && (
                <input
                  type="text"
                  value={form.alarmMessage}
                  onChange={e => updateField('alarmMessage', e.target.value)}
                  placeholder="{entity} entered {geofence}"
                  className="w-full px-2.5 py-1.5 bg-dark-800 border border-dark-600 rounded text-dark-100 text-xs focus:outline-none focus:border-primary-500"
                />
              )}

              {/* Triggers */}
              <div className="flex items-center gap-4">
                <label className="flex items-center gap-1.5 text-xs text-dark-300 cursor-pointer">
                  <input type="checkbox" checked={form.triggerOnEntry} onChange={e => updateField('triggerOnEntry', e.target.checked)} className="rounded border-dark-600 bg-dark-800 text-primary-500" />
                  Entry
                </label>
                <label className="flex items-center gap-1.5 text-xs text-dark-300 cursor-pointer">
                  <input type="checkbox" checked={form.triggerOnExit} onChange={e => updateField('triggerOnExit', e.target.checked)} className="rounded border-dark-600 bg-dark-800 text-primary-500" />
                  Exit
                </label>
              </div>

              {/* Entity filters */}
              <div className="flex flex-wrap items-center gap-3">
                {(['appliesToAdsb', 'appliesToDrones', 'appliesToTargets', 'appliesToDevices'] as const).map(key => (
                  <label key={key} className="flex items-center gap-1 text-xs text-dark-300 cursor-pointer">
                    <input type="checkbox" checked={form[key]} onChange={e => updateField(key, e.target.checked)} className="rounded border-dark-600 bg-dark-800 text-primary-500" />
                    {key.replace('appliesTo', '')}
                  </label>
                ))}
              </div>

              {/* Webhook notification */}
              <label className="flex items-center gap-1.5 text-xs text-dark-300 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.notifyWebhook}
                  onChange={e => updateField('notifyWebhook', e.target.checked)}
                  className="rounded border-dark-600 bg-dark-800 text-primary-500"
                />
                Notify webhook
              </label>

              {/* Drawing status */}
              <div className="bg-dark-800 rounded px-3 py-2 border border-dark-600">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-dark-300">
                    {draftVertices.length} point{draftVertices.length !== 1 ? 's' : ''} drawn
                    {draftVertices.length < 3 && <span className="text-dark-500 ml-1">(min 3)</span>}
                  </span>
                  <div className="flex gap-2">
                    <button
                      onClick={handleUndo}
                      disabled={draftVertices.length === 0}
                      className="text-[10px] px-2 py-0.5 rounded bg-dark-700 text-dark-300 hover:bg-dark-600 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                    >
                      Undo
                    </button>
                    <button
                      onClick={handleClearDraft}
                      disabled={draftVertices.length === 0}
                      className="text-[10px] px-2 py-0.5 rounded bg-dark-700 text-dark-300 hover:bg-dark-600 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                    >
                      Clear
                    </button>
                  </div>
                </div>
                <p className="text-[10px] text-dark-500 mt-1">Click on the map to place vertices</p>
              </div>

              {/* Error */}
              {formError && <p className="text-xs text-red-400">{formError}</p>}

              {/* Actions */}
              <div className="flex items-center gap-2 pt-1">
                <button
                  onClick={handleSubmit}
                  disabled={isPending}
                  className="px-4 py-1.5 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-xs rounded font-medium transition-colors"
                >
                  {isPending ? 'Saving...' : editingId ? 'Update' : 'Create'}
                </button>
                <button
                  onClick={handleCancel}
                  className="px-4 py-1.5 bg-dark-700 hover:bg-dark-600 text-dark-300 text-xs rounded font-medium transition-colors"
                >
                  Cancel
                </button>
              </div>
            </div>
          </div>
        )}

        {/* Geofence list */}
        <div className="flex-1 overflow-y-auto">
          {isLoading ? (
            <div className="p-8 text-center text-dark-400 text-sm">Loading...</div>
          ) : geofences.length === 0 ? (
            <div className="p-8 text-center text-dark-400 text-sm">No geofences defined</div>
          ) : (
            geofences.map((g: Geofence) => (
              <div
                key={g.id}
                className={`px-4 py-3 border-b border-dark-700/30 hover:bg-dark-800/30 transition-colors ${
                  editingId === g.id ? 'bg-dark-800/50' : ''
                }`}
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <div className="w-3 h-3 rounded-sm flex-shrink-0" style={{ backgroundColor: g.color || '#F59E0B' }} />
                    <span className="text-sm text-dark-200 font-medium">{g.name}</span>
                    <span className={`text-[10px] px-1.5 py-0.5 rounded border ${
                      g.alarmEnabled
                        ? 'bg-red-600/20 text-red-400 border-red-500/30'
                        : 'bg-dark-700/50 text-dark-400 border-dark-600'
                    }`}>
                      {g.action}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="text-[10px] text-dark-500 font-mono">{g.polygon?.length || 0} pts</span>
                    <button
                      onClick={() => handleEdit(g)}
                      className="text-[10px] text-primary-400 hover:text-primary-300"
                    >
                      Edit
                    </button>
                    <button
                      onClick={() => { if (confirm(`Delete "${g.name}"?`)) deleteMutation.mutate(g.id) }}
                      className="text-[10px] text-red-400 hover:text-red-300"
                    >
                      Delete
                    </button>
                  </div>
                </div>
                {g.description && <p className="text-[10px] text-dark-500 mt-0.5 truncate">{g.description}</p>}
                <div className="flex gap-3 mt-1 text-[10px] text-dark-500">
                  <span>{[g.triggerOnEntry && 'Entry', g.triggerOnExit && 'Exit'].filter(Boolean).join('/')}</span>
                  <span>{[g.appliesToDrones && 'Drones', g.appliesToAdsb && 'ADS-B', g.appliesToTargets && 'Targets', g.appliesToDevices && 'Devices'].filter(Boolean).join(', ')}</span>
                  {g.alarmLevel && <span className={g.alarmLevel === 'CRITICAL' ? 'text-red-400' : g.alarmLevel === 'ALERT' ? 'text-orange-400' : 'text-dark-400'}>{g.alarmLevel}</span>}
                </div>
              </div>
            ))
          )}
        </div>
      </div>

      {/* Right panel — Map */}
      <div className="flex-1 relative">
        <MapContainer
          center={defaultCenter}
          zoom={13}
          className="w-full h-full"
          style={{ background: '#0f172a', cursor: showForm ? 'crosshair' : '' }}
        >
          <TileLayer
            url="https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png"
            attribution='&copy; <a href="https://carto.com">CARTO</a>'
          />

          <DrawingHandler
            enabled={showForm}
            onPoint={handleMapClick}
            onHover={setHoverPoint}
          />

          {/* Auto-fit to nodes when no geofences and not editing */}
          {!showForm && geofences.length === 0 && nodesWithGPS.length > 0 && (
            <FitToPoints points={nodesWithGPS.map(n => ({ lat: n.latitude!, lng: n.longitude! }))} />
          )}

          {/* Mesh node markers */}
          {nodesWithGPS.map((n) => (
            <CircleMarker
              key={n.id}
              center={[n.latitude!, n.longitude!]}
              radius={6}
              pathOptions={{
                fillColor: n.isOnline ? '#3B82F6' : '#6B7280',
                fillOpacity: 0.9,
                color: '#fff',
                weight: 1.5,
                opacity: 0.7,
              }}
            >
              <Tooltip direction="top" offset={[0, -8]} className="dark-tooltip">
                <span className="text-xs font-mono">
                  {n.longName || n.shortName || n.id}
                  {n.isOnline ? ' (online)' : ' (offline)'}
                </span>
              </Tooltip>
            </CircleMarker>
          ))}

          {/* Fit to vertices when editing */}
          {fitOnce && draftVertices.length > 0 && <FitToPoints points={draftVertices} />}

          {/* Existing geofences (dimmed when drawing) */}
          {geofences
            .filter((g: Geofence) => g.polygon?.length >= 3 && g.id !== editingId)
            .map((g: Geofence) => (
              <Polygon
                key={g.id}
                positions={g.polygon.map(p => [p.lat, p.lng] as [number, number])}
                pathOptions={{
                  fillColor: g.color || '#F59E0B',
                  fillOpacity: showForm ? 0.05 : 0.15,
                  color: g.color || '#F59E0B',
                  weight: showForm ? 1 : 2,
                  opacity: showForm ? 0.3 : 0.7,
                }}
              />
            ))}

          {/* Draft polygon preview */}
          {showForm && draftVertices.length >= 3 && (
            <Polygon
              positions={closedPositions}
              pathOptions={{
                fillColor: form.color,
                fillOpacity: 0.1,
                color: form.color,
                weight: 2,
                opacity: 0.5,
                dashArray: '6 4',
              }}
            />
          )}

          {/* Draft polyline (edges) */}
          {showForm && closedPositions.length >= 2 && (
            <Polyline
              positions={closedPositions}
              pathOptions={{
                color: form.color,
                weight: 2,
                opacity: 0.8,
                dashArray: '6 4',
              }}
            />
          )}

          {/* Draft vertex markers */}
          {showForm && draftVertices.map((p, i) => (
            <CircleMarker
              key={i}
              center={[p.lat, p.lng]}
              radius={5}
              pathOptions={{
                fillColor: form.color,
                fillOpacity: 1,
                color: '#fff',
                weight: 2,
              }}
            />
          ))}
        </MapContainer>

        {/* Drawing mode overlay hint */}
        {showForm && draftVertices.length === 0 && (
          <div className="absolute top-4 left-1/2 -translate-x-1/2 z-[1000] bg-dark-900/90 backdrop-blur-sm px-4 py-2 rounded-lg border border-dark-700/50 text-dark-300 text-xs">
            Click on the map to start drawing a geofence polygon
          </div>
        )}
      </div>
    </div>
  )
}
