import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useMemo, useCallback, useEffect, useRef } from 'react'
import { MapContainer, TileLayer, Marker, Polyline, Popup, useMap, CircleMarker, Polygon } from 'react-leaflet'
import L from 'leaflet'
import api from '../api/client'
import { useDronesStore, type DroneStatus } from '../stores/dronesStore'

// Dark popup style (shared with MapPage via DOM ID check)
if (!document.getElementById('dark-popup-style')) {
  const s = document.createElement('style')
  s.id = 'dark-popup-style'
  s.textContent = `
    .leaflet-popup-content-wrapper { background: #1e293b !important; color: #e2e8f0 !important; border-radius: 8px !important; box-shadow: 0 4px 20px rgba(0,0,0,0.5) !important; }
    .leaflet-popup-tip { background: #1e293b !important; }
    .leaflet-popup-close-button { color: #94a3b8 !important; }
    .leaflet-popup-close-button:hover { color: #e2e8f0 !important; }
    @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.7; } }
  `
  document.head.appendChild(s)
}

interface NodeRow {
  id: string
  nodeNum: number
  nodeType?: string
  name: string
  shortName?: string
  hwModel?: string
  lat?: number
  lon?: number
  batteryLevel?: number
  isOnline: boolean
  isLocal?: boolean
}

interface GeofencePoint { lat: number; lng: number }
interface GeofenceRow {
  id: string
  name: string
  description?: string
  color?: string
  polygon: GeofencePoint[]
  action: string
  enabled: boolean
}

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
  status: DroneStatus
  source?: string
  nodeId?: string
  siteName?: string
  siteColor?: string
  faa?: Record<string, unknown>
  firstSeen?: string
  lastSeen?: string
}

const STATUS_OPTIONS: DroneStatus[] = ['UNKNOWN', 'FRIENDLY', 'NEUTRAL', 'HOSTILE']
const STATUS_COLORS: Record<DroneStatus, string> = {
  UNKNOWN: '#94A3B8',
  FRIENDLY: '#22C55E',
  NEUTRAL: '#F59E0B',
  HOSTILE: '#EF4444',
}

function statusBadgeClass(s: DroneStatus, active = false) {
  const suffix = active ? '-active' : ''
  switch (s) {
    case 'HOSTILE': return `badge-hostile${suffix}`
    case 'FRIENDLY': return `badge-friendly${suffix}`
    case 'NEUTRAL': return `badge-neutral${suffix}`
    default: return `badge-unknown${suffix}`
  }
}

function droneIcon(status: DroneStatus) {
  const color = STATUS_COLORS[status]
  return L.divIcon({
    className: 'custom-drone-icon',
    html: `<div style="
      width: 28px; height: 28px;
      background: ${color}22;
      border: 2px solid ${color};
      border-radius: 50%;
      display: flex; align-items: center; justify-content: center;
      box-shadow: 0 0 12px ${color}55;
      animation: pulse 2s ease-in-out infinite;
    ">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="2">
        <path d="M3.055 11H5a2 2 0 012 2v1a2 2 0 002 2h6a2 2 0 002-2v-1a2 2 0 012-2h1.945M12 3v3m-4.243.757L6.343 5.343m11.314 1.414L16.243 5.343"/>
      </svg>
    </div>`,
    iconSize: [28, 28],
    iconAnchor: [14, 14],
    popupAnchor: [0, -14],
  })
}

function operatorIcon() {
  return L.divIcon({
    className: 'custom-operator-icon',
    html: `<div style="
      width: 22px; height: 22px;
      background: #F5920022;
      border: 2px solid #F59E0B;
      border-radius: 4px;
      display: flex; align-items: center; justify-content: center;
    ">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="#F59E0B" stroke="none">
        <path d="M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z"/>
      </svg>
    </div>`,
    iconSize: [22, 22],
    iconAnchor: [11, 11],
    popupAnchor: [0, -11],
  })
}

// Render FAA registry info matching CC PRO's renderRidInfo pattern
function renderFaaInfo(faa: Record<string, unknown> | undefined, compact = false) {
  if (!faa || Object.keys(faa).length === 0) return null
  const f = faa as Record<string, string>

  // Primary: make + model, fallback to registrant → nNumber → serial
  const make = [f.makeName || f.manufacturer, f.modelName || f.model].filter(Boolean).join(' ')
  const primary = make || f.registrantName || f.nNumber || f.serialNumber || 'Match'

  const fccLabel = f.fccIdentifier || null
  const ridLabel = f.serialNumber || f.nNumber || null
  const registrant = f.registrantName || null
  const location = [f.registrantCity, f.registrantState].filter(Boolean).join(', ')

  if (compact) {
    // For map popups — single line
    return (
      <div>
        <div style={{ color: '#22c55e' }}>{primary}</div>
        {registrant && registrant !== primary && <div>{registrant}</div>}
        {f.nNumber && <div>N-{f.nNumber}</div>}
      </div>
    )
  }

  // Full display for sidebar
  return (
    <div className="text-[10px] bg-emerald-500/10 border border-emerald-500/20 rounded px-2 py-1.5">
      <div className="flex items-center gap-1 mb-0.5">
        <span className="text-emerald-400 font-medium">FAA Registry</span>
        {f.nNumber && <span className="text-dark-400 font-mono">N-{f.nNumber}</span>}
      </div>
      <div className="text-dark-200 font-medium">{primary}</div>
      {registrant && registrant !== primary && (
        <div className="text-dark-300">{registrant}</div>
      )}
      {location && <div className="text-dark-400">{location}</div>}
      {fccLabel && <div className="text-dark-400">FCC: {fccLabel}</div>}
      {ridLabel && ridLabel !== f.nNumber && (
        <div className="text-dark-400 font-mono">S/N: {ridLabel}</div>
      )}
    </div>
  )
}

function formatAge(dateStr?: string): string {
  if (!dateStr) return '-'
  const diff = Date.now() - new Date(dateStr).getTime()
  const sec = Math.floor(diff / 1000)
  if (sec < 60) return `${sec}s ago`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  return `${Math.floor(min / 60)}h ago`
}

function AutoFitBounds({ drones, nodes, selectedId, focusLat, focusLon }: {
  drones: DroneRow[]
  nodes: NodeRow[]
  selectedId: string | null
  focusLat: number
  focusLon: number
}) {
  const map = useMap()
  const fittedRef = useRef(false)

  // Auto-fit on initial load
  useEffect(() => {
    if (fittedRef.current) return
    const pts: [number, number][] = []
    drones.forEach(d => pts.push([d.lat, d.lon]))
    nodes.filter(n => n.lat && n.lon).forEach(n => pts.push([n.lat!, n.lon!]))
    if (pts.length === 0) return
    fittedRef.current = true
    if (pts.length === 1) {
      map.setView(pts[0], 14)
    } else {
      map.fitBounds(L.latLngBounds(pts.map(p => L.latLng(p[0], p[1]))), { padding: [50, 50], maxZoom: 15 })
    }
  }, [drones.length, nodes.length, map])

  // Focus on selected drone
  useEffect(() => {
    if (selectedId && focusLat !== 0 && focusLon !== 0) {
      map.flyTo([focusLat, focusLon], 16, { duration: 0.8 })
    }
  }, [selectedId, focusLat, focusLon, map])

  return null
}

export default function DronesPage() {
  const queryClient = useQueryClient()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const trails = useDronesStore((s) => s.trails)
  const appendTrail = useDronesStore((s) => s.appendTrail)

  const { data: drones = [], isLoading } = useQuery({
    queryKey: ['drones'],
    queryFn: () => api.get<DroneRow[]>('/drones'),
    refetchInterval: 3000,
  })

  // Feed polled drone positions into the trail system
  useEffect(() => {
    drones.forEach(d => {
      if (d.lat !== 0 && d.lon !== 0) {
        appendTrail(d.id, d.lat, d.lon)
      }
    })
  }, [drones, appendTrail])

  const { data: nodes = [] } = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api.get<NodeRow[]>('/nodes'),
    refetchInterval: 10000,
  })

  const { data: geofences = [] } = useQuery({
    queryKey: ['geofences'],
    queryFn: () => api.get<GeofenceRow[]>('/geofences'),
    refetchInterval: 15000,
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

  const sorted = useMemo(() =>
    [...drones].sort((a, b) => {
      const ta = a.lastSeen ? new Date(a.lastSeen).getTime() : 0
      const tb = b.lastSeen ? new Date(b.lastSeen).getTime() : 0
      return tb - ta
    }),
  [drones])

  const selected = sorted.find(d => d.id === selectedId)
  const focusLat = selected?.lat || 0
  const focusLon = selected?.lon || 0

  const droneMarkers = useMemo(() => sorted.filter(d => d.lat !== 0 && d.lon !== 0), [sorted])

  const handleSelect = useCallback((id: string) => {
    setSelectedId(prev => prev === id ? null : id)
  }, [])

  // Status summary counts
  const counts = useMemo(() => {
    const c = { UNKNOWN: 0, FRIENDLY: 0, NEUTRAL: 0, HOSTILE: 0 }
    drones.forEach(d => { c[d.status]++ })
    return c
  }, [drones])

  const defaultCenter: [number, number] = [47.3769, 8.5417]

  return (
    <div className="absolute inset-0 flex">
      {/* Map (main area) */}
      <div className="flex-1 relative">
        <MapContainer
          center={defaultCenter}
          zoom={5}
          className="h-full w-full"
          zoomControl={true}
          attributionControl={false}
        >
          <TileLayer
            url="https://cartodb-basemaps-{s}.global.ssl.fastly.net/dark_all/{z}/{x}/{y}.png"
            subdomains="abcd"
            maxZoom={19}
          />

          {/* Drone markers */}
          {droneMarkers.map((d) => (
            <Marker
              key={`drone-${d.id}`}
              position={[d.lat, d.lon]}
              icon={droneIcon(d.status)}
              eventHandlers={{ click: () => handleSelect(d.id) }}
            >
              <Popup>
                <div className="text-xs min-w-[180px]" style={{ color: '#e2e8f0' }}>
                  <div style={{ fontWeight: 600, fontSize: '13px', marginBottom: '4px', color: '#f1f5f9' }}>
                    {d.droneId?.slice(0, 12) || d.id?.slice(0, 12)}
                  </div>
                  <div style={{ color: '#94a3b8' }}>
                    <div>Status: <span style={{ color: STATUS_COLORS[d.status] }}>{d.status}</span></div>
                    {d.mac && <div>MAC: {d.mac}</div>}
                    {d.altitude !== undefined && d.altitude > 0 && <div>Alt: {d.altitude.toFixed(0)}m</div>}
                    {d.speed !== undefined && d.speed > 0 && <div>Speed: {d.speed.toFixed(1)} m/s</div>}
                    {d.heading !== undefined && d.heading > 0 && <div>Heading: {d.heading.toFixed(0)}&deg;</div>}
                    {d.rssi !== undefined && d.rssi !== 0 && <div>RSSI: {d.rssi} dBm</div>}
                    {(d.uaType || d.manufacturer) && <div>Type: {d.uaType || d.manufacturer}</div>}
                    {d.faa && renderFaaInfo(d.faa, true)}
                  </div>
                </div>
              </Popup>
            </Marker>
          ))}

          {/* Operator markers with dashed lines to drone */}
          {droneMarkers.filter(d => d.operatorLat && d.operatorLon && d.operatorLat !== 0).map((d) => (
            <span key={`op-${d.id}`}>
              <Marker
                position={[d.operatorLat!, d.operatorLon!]}
                icon={operatorIcon()}
              >
                <Popup>
                  <div className="text-xs" style={{ color: '#e2e8f0' }}>
                    <div style={{ fontWeight: 600, color: '#f1f5f9' }}>Operator</div>
                    <div style={{ color: '#94a3b8' }}>
                      {d.operatorLat!.toFixed(5)}, {d.operatorLon!.toFixed(5)}
                    </div>
                  </div>
                </Popup>
              </Marker>
              <Polyline
                positions={[[d.operatorLat!, d.operatorLon!], [d.lat, d.lon]]}
                pathOptions={{ color: '#F59E0B', weight: 1.5, opacity: 0.6, dashArray: '6 4' }}
              />
            </span>
          ))}

          {/* Flight trails */}
          {droneMarkers.map((d) => {
            const trail = trails.get(d.id)
            if (!trail || trail.length < 2) return null
            const positions = trail.map(p => [p.lat, p.lon] as [number, number])
            return (
              <Polyline
                key={`trail-${d.id}`}
                positions={positions}
                pathOptions={{
                  color: STATUS_COLORS[d.status],
                  weight: 2,
                  opacity: 0.5,
                }}
              />
            )
          })}

          {/* Mesh nodes (blue/orange dots) */}
          {nodes.filter((n: NodeRow) => n.lat && n.lon && (n.lat !== 0 || n.lon !== 0)).map((n: NodeRow) => {
            const isAH = n.nodeType === 'antihunter'
            const fill = !n.isOnline ? '#475569' : isAH ? '#F97316' : '#3B82F6'
            const stroke = !n.isOnline ? '#64748B' : isAH ? '#FB923C' : '#60A5FA'
            return (
              <CircleMarker
                key={`node-${n.id}`}
                center={[n.lat!, n.lon!]}
                radius={7}
                pathOptions={{ fillColor: fill, fillOpacity: n.isOnline ? 0.7 : 0.4, color: stroke, weight: 2 }}
              >
                <Popup>
                  <div className="text-xs min-w-[140px]" style={{ color: '#e2e8f0' }}>
                    <div style={{ fontWeight: 600, fontSize: '13px', color: '#f1f5f9' }}>
                      {n.name || n.shortName || `Node ${n.nodeNum}`}
                    </div>
                    <div style={{ color: '#94a3b8' }}>
                      <div>HW: {n.hwModel || 'Unknown'}</div>
                      <div>Status: <span style={{ color: n.isOnline ? '#22C55E' : '#94A3B8' }}>{n.isOnline ? 'Online' : 'Offline'}</span></div>
                      {n.batteryLevel !== undefined && n.batteryLevel > 0 && <div>Battery: {n.batteryLevel}%</div>}
                    </div>
                  </div>
                </Popup>
              </CircleMarker>
            )
          })}

          {/* Geofences */}
          {geofences.filter((g: GeofenceRow) => g.enabled && g.polygon && g.polygon.length >= 3).map((g: GeofenceRow) => (
            <Polygon
              key={`gf-${g.id}`}
              positions={g.polygon.map(p => [p.lat, p.lng] as [number, number])}
              pathOptions={{ fillColor: g.color || '#F59E0B', fillOpacity: 0.15, color: g.color || '#F59E0B', weight: 2, opacity: 0.7 }}
            >
              <Popup>
                <div className="text-xs" style={{ color: '#e2e8f0' }}>
                  <div style={{ fontWeight: 600, color: '#f1f5f9' }}>{g.name}</div>
                  {g.description && <div style={{ color: '#94a3b8' }}>{g.description}</div>}
                </div>
              </Popup>
            </Polygon>
          ))}

          {/* Auto-fit bounds on load */}
          <AutoFitBounds drones={droneMarkers} nodes={nodes} selectedId={selectedId} focusLat={focusLat} focusLon={focusLon} />
        </MapContainer>

        {/* Status summary overlay */}
        <div className="absolute top-3 left-3 z-[400] flex gap-2">
          {(Object.entries(counts) as [DroneStatus, number][]).filter(([, c]) => c > 0).map(([status, count]) => (
            <div key={status} className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-surface/90 backdrop-blur-sm border border-dark-700/50">
              <span className="w-2.5 h-2.5 rounded-full" style={{ backgroundColor: STATUS_COLORS[status] }} />
              <span className="text-xs font-medium text-dark-200">{count}</span>
              <span className="text-[10px] text-dark-400 uppercase">{status}</span>
            </div>
          ))}
        </div>

        {/* No drones overlay */}
        {!isLoading && drones.length === 0 && (
          <div className="absolute inset-0 flex items-center justify-center pointer-events-none z-[400]">
            <div className="bg-surface/90 backdrop-blur-sm rounded-xl border border-dark-700/50 px-6 py-4 text-center">
              <p className="text-dark-400 text-sm">No drones detected</p>
              <p className="text-dark-500 text-xs mt-1">Drone telemetry from mesh sensors will appear here</p>
            </div>
          </div>
        )}
      </div>

      {/* Sidebar table */}
      <div className="w-96 border-l border-dark-700/50 bg-surface flex flex-col overflow-hidden">
        {/* Sidebar header */}
        <div className="px-4 py-3 border-b border-dark-700/50 flex items-center justify-between shrink-0">
          <div>
            <h2 className="text-sm font-semibold text-dark-100">Drone Tracker</h2>
            <p className="text-[11px] text-dark-500 mt-0.5">
              {drones.length} detection{drones.length !== 1 ? 's' : ''}
            </p>
          </div>
          <button
            onClick={() => { if (drones.length > 0 && confirm('Clear all drones?')) clearAll.mutate() }}
            disabled={drones.length === 0}
            className="text-[10px] px-2 py-1 rounded bg-dark-800 text-dark-400 hover:bg-dark-700 disabled:opacity-40 transition-colors border border-dark-700/50"
          >
            Clear
          </button>
        </div>

        {/* Drone list */}
        <div className="flex-1 overflow-y-auto">
          {isLoading ? (
            <div className="p-8 text-center text-dark-500 text-sm">Loading...</div>
          ) : sorted.length === 0 ? (
            <div className="p-8 text-center text-dark-500 text-sm">No drones detected</div>
          ) : sorted.map((d) => (
            <div
              key={d.id}
              className={`border-b border-dark-700/30 cursor-pointer transition-colors ${
                selectedId === d.id ? 'bg-dark-800/60' : 'hover:bg-dark-800/30'
              }`}
              onClick={() => handleSelect(d.id)}
            >
              {/* Main row */}
              <div className="px-3 py-2.5 flex items-center gap-2">
                {/* Status dot */}
                <span
                  className="w-2.5 h-2.5 rounded-full shrink-0"
                  style={{ backgroundColor: STATUS_COLORS[d.status] }}
                />
                {/* ID + MAC + FAA badge */}
                <div className="flex-1 min-w-0">
                  <div className="text-xs text-dark-200 font-mono truncate flex items-center gap-1">
                    {d.droneId?.slice(0, 14) || d.id?.slice(0, 14)}
                    {d.faa && Object.keys(d.faa).length > 0 && (
                      <span className="text-[8px] px-1 py-0 rounded bg-emerald-500/20 text-emerald-400 font-sans shrink-0">FAA</span>
                    )}
                  </div>
                  <div className="text-[10px] text-dark-500 truncate">
                    {d.faa && (d.faa as Record<string, string>).registrantName
                      ? (d.faa as Record<string, string>).registrantName
                      : <span className="font-mono">{d.mac || d.serialNumber || d.uasId || '-'}</span>
                    }
                  </div>
                </div>
                {/* RSSI */}
                <div className="text-[10px] text-dark-400 font-mono w-10 text-right">
                  {d.rssi ?? '-'}
                </div>
                {/* Age */}
                <div className="text-[10px] text-dark-500 w-12 text-right">
                  {formatAge(d.lastSeen)}
                </div>
                {/* Delete */}
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    if (confirm(`Remove drone ${d.droneId || d.id}?`)) deleteDrone.mutate(d.id)
                  }}
                  className="text-dark-600 hover:text-status-hostile transition-colors shrink-0"
                  title="Remove"
                >
                  <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>

              {/* Expanded detail */}
              {selectedId === d.id && (
                <div className="px-3 pb-3 space-y-2">
                  {/* Telemetry grid */}
                  <div className="grid grid-cols-3 gap-x-3 gap-y-1.5 text-[10px]">
                    <div>
                      <span className="text-dark-500 block">Alt</span>
                      <span className="text-dark-300 font-mono">{d.altitude?.toFixed(0) ?? '-'}m</span>
                    </div>
                    <div>
                      <span className="text-dark-500 block">Speed</span>
                      <span className="text-dark-300 font-mono">{d.speed?.toFixed(1) ?? '-'} m/s</span>
                    </div>
                    <div>
                      <span className="text-dark-500 block">Heading</span>
                      <span className="text-dark-300 font-mono">{d.heading?.toFixed(0) ?? '-'}&deg;</span>
                    </div>
                    <div>
                      <span className="text-dark-500 block">Position</span>
                      <span className="text-dark-300 font-mono">
                        {d.lat ? `${d.lat.toFixed(5)}` : '-'}, {d.lon ? `${d.lon.toFixed(5)}` : '-'}
                      </span>
                    </div>
                    <div>
                      <span className="text-dark-500 block">Operator</span>
                      <span className="text-dark-300 font-mono">
                        {d.operatorLat ? `${d.operatorLat.toFixed(4)}, ${d.operatorLon?.toFixed(4)}` : '-'}
                      </span>
                    </div>
                    <div>
                      <span className="text-dark-500 block">Source</span>
                      <span className="text-dark-300">{d.source || '-'}</span>
                    </div>
                  </div>

                  {/* FAA registry data */}
                  {renderFaaInfo(d.faa)}

                  {/* Status buttons */}
                  <div>
                    <span className="text-[10px] text-dark-500 block mb-1">Classification</span>
                    <div className="flex gap-1">
                      {STATUS_OPTIONS.map((s) => (
                        <button
                          key={s}
                          onClick={(e) => {
                            e.stopPropagation()
                            updateStatus.mutate({ id: d.id, status: s })
                          }}
                          className={`px-2 py-0.5 rounded text-[10px] font-medium transition-colors ${
                            d.status === s
                              ? statusBadgeClass(s, true)
                              : 'bg-dark-700/50 text-dark-400 hover:bg-dark-700 border border-transparent'
                          }`}
                        >
                          {s}
                        </button>
                      ))}
                    </div>
                  </div>

                  {/* Focus button */}
                  {d.lat !== 0 && d.lon !== 0 && (
                    <button
                      onClick={(e) => { e.stopPropagation(); handleSelect(d.id) }}
                      className="text-[10px] text-primary-400 hover:text-primary-300 transition-colors"
                    >
                      Center on map
                    </button>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
