import { useQuery } from '@tanstack/react-query'
import { MapContainer, TileLayer, CircleMarker, Marker, Popup, useMap, LayersControl, Polygon, Circle, LayerGroup } from 'react-leaflet'
import L from 'leaflet'
import { useEffect, useMemo } from 'react'
import api from '../api/client'

// Dark popup style override
const darkPopupStyle = document.createElement('style')
darkPopupStyle.textContent = `
  .leaflet-popup-content-wrapper {
    background: #1e293b !important;
    color: #e2e8f0 !important;
    border-radius: 8px !important;
    box-shadow: 0 4px 20px rgba(0,0,0,0.5) !important;
  }
  .leaflet-popup-tip {
    background: #1e293b !important;
  }
  .leaflet-popup-close-button {
    color: #94a3b8 !important;
  }
  .leaflet-popup-close-button:hover {
    color: #e2e8f0 !important;
  }
`
if (!document.getElementById('dark-popup-style')) {
  darkPopupStyle.id = 'dark-popup-style'
  document.head.appendChild(darkPopupStyle)
}

interface DroneRow {
  id: string
  droneId: string
  mac?: string
  uaType?: string
  manufacturer?: string
  lat: number
  lon: number
  altitude?: number
  speed?: number
  heading?: number
  rssi?: number
  status: string
  source?: string
  siteName?: string
  operatorLat?: number
  operatorLon?: number
  faa?: Record<string, string>
  lastSeen?: string
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
  altitude?: number
  batteryLevel?: number
  isOnline: boolean
  isLocal?: boolean
  lastHeard?: string
  siteName?: string
}

function nodeColor(n: NodeRow): { fill: string; stroke: string } {
  if (!n.isOnline) return { fill: '#475569', stroke: '#64748B' }
  if (n.nodeType === 'antihunter') return { fill: '#F97316', stroke: '#FB923C' }
  return { fill: '#3B82F6', stroke: '#60A5FA' } // gotailme default
}

function nodeTypeLabel(t?: string): string {
  if (t === 'gotailme') return 'GTM'
  if (t === 'antihunter') return 'AH'
  return '?'
}

interface GeofencePoint {
  lat: number
  lng: number
}

interface Geofence {
  id: string
  name: string
  description?: string
  color?: string
  polygon: GeofencePoint[]
  action: string
  enabled: boolean
  alarmEnabled: boolean
  alarmLevel?: string
  triggerOnEntry: boolean
  triggerOnExit: boolean
}

interface Target {
  id: string
  name: string
  description?: string
  targetType?: string
  mac?: string
  latitude?: number
  longitude?: number
  status: string
  trackingConfidence?: number | null
  trackingUncertainty?: number | null
  triangulationMethod?: string
}

interface Aircraft {
  hex: string
  flight?: string
  lat?: number
  lon?: number
  alt_baro?: number
  gs?: number
  track?: number
  squawk?: string
}

interface ADSBStatus {
  enabled: boolean
}

// --- Map marker icon SVGs by type ---
// Drone: quadcopter silhouette (4 rotors)
const DRONE_SVG = `<path d="M12 2v2m0 16v2M2 12h2m16 0h2M6.3 6.3l1.4 1.4m8.6 8.6l1.4 1.4M17.7 6.3l-1.4 1.4M7.7 15.7l-1.4 1.4M12 8a4 4 0 100 8 4 4 0 000-8z"/>`
// WiFi: signal waves
const WIFI_SVG = `<path d="M12 20h.01M8.53 16.11a6 6 0 018.94 0M5.64 12.72a10 10 0 0112.72 0M2.1 9.32a14 14 0 0119.8 0"/>`
// BLE: bluetooth symbol
const BLE_SVG = `<path d="M7 7l10 10-5 5V2l5 5L7 17"/>`
// Person: user silhouette
const PERSON_SVG = `<path d="M12 12a4 4 0 100-8 4 4 0 000 8zm0 2c-4 0-8 2-8 4v2h16v-2c0-2-4-4-8-4z"/>`
// Vehicle: car
const VEHICLE_SVG = `<path d="M5 17h14M5 17a2 2 0 01-2-2v-3l2-5h10l2 5v3a2 2 0 01-2 2M5 17a2 2 0 002 2m10-2a2 2 0 002 2M7 13h.01M17 13h.01"/>`
// Generic crosshair
const TARGET_SVG = `<circle cx="12" cy="12" r="3"/><path d="M12 2v4m0 12v4M2 12h4m12 0h4"/>`
// Aircraft: airplane
const AIRCRAFT_SVG = `<path d="M12 2L9 9H2l3 5-1 8 8-4 8 4-1-8 3-5h-7z"/>`

// Status/threat color mapping for drones
function droneStatusColor(status: string): string {
  switch (status) {
    case 'HOSTILE': return '#EF4444'
    case 'FRIENDLY': return '#22C55E'
    case 'NEUTRAL': return '#F59E0B'
    default: return '#94A3B8'
  }
}

// Target type color: active = orange, resolved = gray, triangulating = blue
function targetStatusColor(status: string): string {
  switch (status) {
    case 'active': return '#F97316'
    case 'triangulating': return '#3B82F6'
    case 'resolved': return '#64748B'
    default: return '#F97316'
  }
}

// SVG icon by target type
function targetTypeSvg(targetType?: string): string {
  switch (targetType) {
    case 'wifi': return WIFI_SVG
    case 'ble': return BLE_SVG
    case 'drone': return DRONE_SVG
    case 'person': return PERSON_SVG
    case 'vehicle': return VEHICLE_SVG
    default: return TARGET_SVG
  }
}

function makeIcon(svg: string, color: string, size: number, shape: 'circle' | 'square' | 'diamond' = 'circle') {
  const border = shape === 'diamond' ? `transform: rotate(45deg);` : shape === 'square' ? `border-radius: 4px;` : `border-radius: 50%;`
  const inner = shape === 'diamond'
    ? `<div style="transform: rotate(-45deg); display:flex; align-items:center; justify-content:center; width:100%; height:100%;"><svg width="${size*0.5}" height="${size*0.5}" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${svg}</svg></div>`
    : `<svg width="${size*0.5}" height="${size*0.5}" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${svg}</svg>`

  return L.divIcon({
    className: '',
    html: `<div style="
      width: ${size}px; height: ${size}px;
      background: ${color}18;
      border: 2px solid ${color};
      ${border}
      display: flex; align-items: center; justify-content: center;
      box-shadow: 0 0 8px ${color}44;
    ">${inner}</div>`,
    iconSize: [size, size],
    iconAnchor: [size/2, size/2],
    popupAnchor: [0, -size/2],
  })
}

function droneIcon(status: string) {
  return makeIcon(DRONE_SVG, droneStatusColor(status), 28)
}

function targetIcon(targetType?: string, status?: string) {
  const color = targetStatusColor(status || 'active')
  const svg = targetTypeSvg(targetType)
  return makeIcon(svg, color, 26, 'diamond')
}

function aircraftIcon() {
  return makeIcon(AIRCRAFT_SVG, '#A855F7', 24)
}

// Fit bounds to all visible markers
function FitBounds({ positions }: { positions: [number, number][] }) {
  const map = useMap()

  useEffect(() => {
    if (positions.length === 0) return
    if (positions.length === 1) {
      map.setView(positions[0], 14)
      return
    }
    const bounds = L.latLngBounds(positions.map(p => L.latLng(p[0], p[1])))
    map.fitBounds(bounds, { padding: [50, 50], maxZoom: 15 })
  }, [positions.length]) // Only re-fit when count changes

  return null
}

export default function MapPage() {
  const { data: drones = [] } = useQuery({
    queryKey: ['drones'],
    queryFn: () => api.get<DroneRow[]>('/drones'),
    refetchInterval: 5000,
  })

  const { data: nodes = [] } = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api.get<NodeRow[]>('/nodes'),
    refetchInterval: 5000,
  })

  const { data: geofences = [] } = useQuery({
    queryKey: ['geofences'],
    queryFn: () => api.get<Geofence[]>('/geofences'),
    refetchInterval: 10000,
  })

  const { data: targets = [] } = useQuery({
    queryKey: ['targets'],
    queryFn: () => api.get<Target[]>('/targets'),
    refetchInterval: 5000,
  })

  const { data: adsbStatus } = useQuery<ADSBStatus>({
    queryKey: ['adsb-status'],
    queryFn: () => api.get('/adsb/status'),
    refetchInterval: 10000,
  })

  const { data: aircraft = [] } = useQuery({
    queryKey: ['adsb-tracks'],
    queryFn: () => api.get<Aircraft[]>('/adsb/tracks'),
    refetchInterval: 3000,
    enabled: !!adsbStatus?.enabled,
  })

  // Filter entities with valid positions
  const droneMarkers = drones.filter((d: DroneRow) => d.lat !== 0 && d.lon !== 0)
  const nodeMarkers = nodes.filter((n: NodeRow) => n.lat && n.lon && (n.lat !== 0 || n.lon !== 0))
  const targetMarkers = targets.filter((t: Target) => t.latitude && t.longitude && (t.latitude !== 0 || t.longitude !== 0))
  const aircraftMarkers = aircraft.filter((a: Aircraft) => a.lat && a.lon && (a.lat !== 0 || a.lon !== 0))

  // All positions for auto-fit
  const allPositions: [number, number][] = useMemo(() => {
    const pts: [number, number][] = []
    droneMarkers.forEach((d: DroneRow) => pts.push([d.lat, d.lon]))
    nodeMarkers.forEach((n: NodeRow) => {
      if (n.lat && n.lon) pts.push([n.lat, n.lon])
    })
    targetMarkers.forEach((t: Target) => {
      if (t.latitude && t.longitude) pts.push([t.latitude, t.longitude])
    })
    aircraftMarkers.forEach((a: Aircraft) => {
      if (a.lat && a.lon) pts.push([a.lat, a.lon])
    })
    return pts
  }, [droneMarkers.length, nodeMarkers.length, targetMarkers.length, aircraftMarkers.length])

  const defaultCenter: [number, number] = [47.3769, 8.5417] // Zurich fallback
  const defaultZoom = 5

  return (
    <div className="absolute inset-0 flex flex-col">
      {/* Map header bar */}
      <div className="px-4 py-2.5 border-b border-dark-700/50 bg-surface/80 backdrop-blur-sm flex items-center justify-between z-10">
        <h2 className="text-sm font-semibold text-dark-100">
          Map
        </h2>
        <div className="flex items-center gap-4 text-xs">
          {/* Legend */}
          <div className="flex items-center gap-1.5">
            <span className="inline-block w-3 h-3 rounded-full border-2 border-status-hostile bg-status-hostile/20" />
            <span className="text-dark-400">Drones ({droneMarkers.length})</span>
          </div>
          <div className="flex items-center gap-1.5">
            <span className="inline-block w-3 h-3 rounded-full" style={{ backgroundColor: '#3B82F6' }} />
            <span className="text-dark-400">GTM ({nodeMarkers.filter((n: NodeRow) => n.nodeType !== 'antihunter').length})</span>
          </div>
          <div className="flex items-center gap-1.5">
            <span className="inline-block w-3 h-3 rounded-full" style={{ backgroundColor: '#F97316' }} />
            <span className="text-dark-400">AH ({nodeMarkers.filter((n: NodeRow) => n.nodeType === 'antihunter').length})</span>
          </div>
          <div className="flex items-center gap-1.5">
            <span className="inline-block w-3 h-3 bg-orange-500" style={{ transform: 'rotate(45deg)', width: '10px', height: '10px' }} />
            <span className="text-dark-400">Targets ({targetMarkers.length})</span>
          </div>
          {adsbStatus?.enabled && (
            <div className="flex items-center gap-1.5">
              <span className="inline-block w-3 h-3 rounded-full bg-purple-500" />
              <span className="text-dark-400">Aircraft ({aircraftMarkers.length})</span>
            </div>
          )}
        </div>
      </div>

      {/* Map container */}
      <div className="flex-1 relative">
        <MapContainer
          center={defaultCenter}
          zoom={defaultZoom}
          className="h-full w-full"
          zoomControl={true}
          attributionControl={false}
        >
          <LayersControl position="topright">
            {/* Base layers */}
            <LayersControl.BaseLayer checked name="CartoDB Dark">
              <TileLayer
                url="https://cartodb-basemaps-{s}.global.ssl.fastly.net/dark_all/{z}/{x}/{y}.png"
                subdomains="abcd"
                maxZoom={19}
              />
            </LayersControl.BaseLayer>
            <LayersControl.BaseLayer name="OpenStreetMap">
              <TileLayer
                url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
                subdomains="abc"
                maxZoom={19}
              />
            </LayersControl.BaseLayer>
            <LayersControl.BaseLayer name="Esri Satellite">
              <TileLayer
                url="https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}"
                maxZoom={18}
              />
            </LayersControl.BaseLayer>

            {/* Overlay layers */}
            <LayersControl.Overlay checked name="Drones">
              <LayerGroup>
                {droneMarkers.map((d: DroneRow) => (
                  <Marker
                    key={`drone-${d.id}`}
                    position={[d.lat, d.lon]}
                    icon={droneIcon(d.status)}
                  >
                    <Popup>
                      <div className="text-xs min-w-[180px]" style={{ color: '#e2e8f0' }}>
                        <div style={{ fontWeight: 600, fontSize: '13px', marginBottom: '4px', color: '#f1f5f9' }}>
                          Drone: {d.droneId?.slice(0, 12) || d.id?.slice(0, 12)}
                        </div>
                        <div style={{ color: '#94a3b8' }}>
                          <div>Status: <span style={{
                            color: droneStatusColor(d.status)
                          }}>{d.status}</span></div>
                          {d.mac && <div>MAC: {d.mac}</div>}
                          {d.altitude !== undefined && d.altitude > 0 && (
                            <div>Alt: {d.altitude.toFixed(0)}m</div>
                          )}
                          {d.speed !== undefined && d.speed > 0 && (
                            <div>Speed: {d.speed.toFixed(1)} m/s</div>
                          )}
                          {d.heading !== undefined && d.heading > 0 && (
                            <div>Heading: {d.heading.toFixed(0)}&deg;</div>
                          )}
                          {d.rssi !== undefined && d.rssi !== 0 && (
                            <div>RSSI: {d.rssi} dBm</div>
                          )}
                          {(d.uaType || d.manufacturer) && (
                            <div>Type: {d.uaType || d.manufacturer}</div>
                          )}
                          {d.operatorLat !== undefined && d.operatorLat !== 0 && (
                            <div>Operator: {d.operatorLat?.toFixed(5)}, {d.operatorLon?.toFixed(5)}</div>
                          )}
                          {d.faa && Object.keys(d.faa).length > 0 && (
                            <div style={{ marginTop: '3px', borderTop: '1px solid #334155', paddingTop: '3px', color: '#22C55E' }}>
                              <div>{[d.faa.makeName || d.faa.manufacturer, d.faa.modelName || d.faa.model].filter(Boolean).join(' ') || 'FAA Match'}</div>
                              {d.faa.registrantName && <div style={{ color: '#94a3b8' }}>{d.faa.registrantName}</div>}
                              {d.faa.nNumber && <div style={{ color: '#64748B' }}>N-{d.faa.nNumber}</div>}
                            </div>
                          )}
                          {d.source && <div>Source: {d.source}</div>}
                          {d.lastSeen && <div style={{ marginTop: '2px', fontSize: '10px', color: '#64748B' }}>Last: {new Date(d.lastSeen).toLocaleString()}</div>}
                        </div>
                      </div>
                    </Popup>
                  </Marker>
                ))}
              </LayerGroup>
            </LayersControl.Overlay>

            <LayersControl.Overlay checked name="Nodes">
              <LayerGroup>
                {nodeMarkers.map((n: NodeRow) => {
                  const c = nodeColor(n)
                  return (
                    <CircleMarker
                      key={`node-${n.id}`}
                      center={[n.lat!, n.lon!]}
                      radius={8}
                      pathOptions={{
                        fillColor: c.fill,
                        fillOpacity: n.isOnline ? 0.7 : 0.4,
                        color: c.stroke,
                        weight: 2,
                      }}
                    >
                      <Popup>
                        <div className="text-xs min-w-[180px]" style={{ color: '#e2e8f0' }}>
                          <div style={{ fontWeight: 600, fontSize: '13px', marginBottom: '4px', color: '#f1f5f9', display: 'flex', alignItems: 'center', gap: '6px' }}>
                            {n.name || n.shortName || `Node ${n.nodeNum}`}
                            <span style={{
                              fontSize: '9px',
                              padding: '1px 5px',
                              borderRadius: '3px',
                              fontFamily: 'monospace',
                              backgroundColor: n.nodeType === 'antihunter' ? 'rgba(249,115,22,0.2)' : 'rgba(59,130,246,0.2)',
                              color: n.nodeType === 'antihunter' ? '#FB923C' : '#60A5FA',
                              border: `1px solid ${n.nodeType === 'antihunter' ? 'rgba(249,115,22,0.3)' : 'rgba(59,130,246,0.3)'}`,
                            }}>
                              {nodeTypeLabel(n.nodeType)}
                            </span>
                            {n.isLocal && (
                              <span style={{ fontSize: '9px', padding: '1px 5px', borderRadius: '3px', fontFamily: 'monospace', backgroundColor: 'rgba(100,116,139,0.3)', color: '#94a3b8', border: '1px solid rgba(100,116,139,0.3)' }}>
                                LOCAL
                              </span>
                            )}
                          </div>
                          <div style={{ color: '#94a3b8' }}>
                            <div>HW: {n.hwModel || 'Unknown'}</div>
                            <div>Status: <span style={{ color: n.isOnline ? '#22C55E' : '#94A3B8' }}>{n.isOnline ? 'Online' : 'Offline'}</span></div>
                            {n.batteryLevel !== undefined && n.batteryLevel > 0 && (
                              <div>Battery: {n.batteryLevel}%</div>
                            )}
                            {n.altitude !== undefined && n.altitude > 0 && (
                              <div>Alt: {n.altitude.toFixed(0)}m</div>
                            )}
                            {n.siteName && <div>Site: {n.siteName}</div>}
                            {n.lastHeard && <div style={{ marginTop: '2px', fontSize: '10px', color: '#64748B' }}>Last: {new Date(n.lastHeard).toLocaleString()}</div>}
                          </div>
                        </div>
                      </Popup>
                    </CircleMarker>
                  )
                })}
              </LayerGroup>
            </LayersControl.Overlay>

            <LayersControl.Overlay checked name="Node Coverage">
              <LayerGroup>
                {nodeMarkers.map((n: NodeRow) => {
                  const c = nodeColor(n)
                  return (
                    <Circle
                      key={`coverage-${n.id}`}
                      center={[n.lat!, n.lon!]}
                      radius={50}
                      interactive={false}
                      pathOptions={{
                        fillColor: c.fill,
                        fillOpacity: 0.1,
                        color: c.fill,
                        weight: 1,
                        opacity: 0.3,
                      }}
                    />
                  )
                })}
              </LayerGroup>
            </LayersControl.Overlay>

            <LayersControl.Overlay checked name="Geofences">
              <LayerGroup>
                {geofences.filter((g: Geofence) => g.enabled && g.polygon && g.polygon.length >= 3).map((g: Geofence) => (
                  <Polygon
                    key={`geofence-${g.id}`}
                    positions={g.polygon.map(p => [p.lat, p.lng] as [number, number])}
                    pathOptions={{
                      fillColor: g.color || '#F59E0B',
                      fillOpacity: 0.15,
                      color: g.color || '#F59E0B',
                      weight: 2,
                      opacity: 0.7,
                    }}
                  >
                    <Popup>
                      <div className="text-xs min-w-[140px]" style={{ color: '#e2e8f0' }}>
                        <div style={{ fontWeight: 600, fontSize: '13px', marginBottom: '4px', color: '#f1f5f9' }}>
                          {g.name}
                        </div>
                        <div style={{ color: '#94a3b8' }}>
                          {g.description && <div>{g.description}</div>}
                          <div>Action: {g.action}</div>
                          <div>Trigger: {[g.triggerOnEntry && 'Entry', g.triggerOnExit && 'Exit'].filter(Boolean).join(' / ') || '-'}</div>
                          {g.alarmLevel && <div>Alarm: {g.alarmLevel}</div>}
                        </div>
                      </div>
                    </Popup>
                  </Polygon>
                ))}
              </LayerGroup>
            </LayersControl.Overlay>

            <LayersControl.Overlay checked name="Targets">
              <LayerGroup>
                {targetMarkers.map((t: Target) => (
                  <Marker
                    key={`target-${t.id}`}
                    position={[t.latitude!, t.longitude!]}
                    icon={targetIcon(t.targetType, t.status)}
                  >
                    <Popup>
                      <div className="text-xs min-w-[160px]" style={{ color: '#e2e8f0' }}>
                        <div style={{ fontWeight: 600, fontSize: '13px', marginBottom: '4px', color: '#f1f5f9' }}>
                          {t.name}
                        </div>
                        <div style={{ color: '#94a3b8' }}>
                          {t.description && <div>{t.description}</div>}
                          <div>Type: {t.targetType || '-'}</div>
                          {t.mac && <div>MAC: {t.mac}</div>}
                          <div>Status: <span style={{ color: t.status === 'active' ? '#22C55E' : t.status === 'triangulating' ? '#3B82F6' : '#94A3B8' }}>{t.status}</span></div>
                          {t.trackingConfidence != null && t.trackingConfidence > 0 && (
                            <div>Confidence: <span style={{
                              color: t.trackingConfidence > 0.7 ? '#22C55E' : t.trackingConfidence > 0.5 ? '#F59E0B' : '#EF4444'
                            }}>{Math.round(t.trackingConfidence * 100)}%</span>
                            {t.trackingUncertainty != null && t.trackingUncertainty > 0 && (
                              <span> &plusmn;{Math.round(t.trackingUncertainty)}m</span>
                            )}
                            </div>
                          )}
                          {t.triangulationMethod && <div style={{ fontSize: '10px', color: '#64748B' }}>{t.triangulationMethod}</div>}
                        </div>
                      </div>
                    </Popup>
                  </Marker>
                ))}
              </LayerGroup>
            </LayersControl.Overlay>

            {adsbStatus?.enabled && (
              <LayersControl.Overlay checked name="ADS-B Aircraft">
                <LayerGroup>
                  {aircraftMarkers.map((a: Aircraft) => (
                    <Marker
                      key={`aircraft-${a.hex}`}
                      position={[a.lat!, a.lon!]}
                      icon={aircraftIcon()}
                    >
                      <Popup>
                        <div className="text-xs min-w-[140px]" style={{ color: '#e2e8f0' }}>
                          <div style={{ fontWeight: 600, fontSize: '13px', marginBottom: '4px', color: '#f1f5f9' }}>
                            {a.flight?.trim() || a.hex}
                          </div>
                          <div style={{ color: '#94a3b8' }}>
                            <div>ICAO: {a.hex}</div>
                            {a.alt_baro !== undefined && <div>Alt: {a.alt_baro.toLocaleString()} ft</div>}
                            {a.gs !== undefined && <div>Speed: {Math.round(a.gs)} kts</div>}
                            {a.track !== undefined && <div>Track: {Math.round(a.track)}&deg;</div>}
                            {a.squawk && <div>Squawk: {a.squawk}</div>}
                          </div>
                        </div>
                      </Popup>
                    </Marker>
                  ))}
                </LayerGroup>
              </LayersControl.Overlay>
            )}
          </LayersControl>

          <FitBounds positions={allPositions} />
        </MapContainer>

        {/* Overlay stats when no markers */}
        {droneMarkers.length === 0 && nodeMarkers.length === 0 && targetMarkers.length === 0 && aircraftMarkers.length === 0 && (
          <div className="absolute inset-0 flex items-center justify-center pointer-events-none z-[400]">
            <div className="bg-surface/90 backdrop-blur-sm rounded-xl border border-dark-700/50 px-6 py-4 text-center">
              <p className="text-dark-400 text-sm">No positioned entities</p>
              <p className="text-dark-500 text-xs mt-1">Drones, nodes, targets, and aircraft with GPS data will appear here</p>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
