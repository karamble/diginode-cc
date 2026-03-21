import { useQuery } from '@tanstack/react-query'
import { MapContainer, TileLayer, CircleMarker, Marker, Popup, useMap } from 'react-leaflet'
import L from 'leaflet'
import { useEffect, useMemo } from 'react'
import api from '../api/client'

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
  lastSeen?: string
}

interface NodeRow {
  id: string
  nodeNum: number
  name: string
  shortName?: string
  hwModel?: string
  lat?: number
  lon?: number
  altitude?: number
  batteryLevel?: number
  isOnline: boolean
  lastHeard?: string
  siteName?: string
}

// Custom red icon for drones
function droneIcon(status: string) {
  const color = status === 'HOSTILE' ? '#EF4444' : status === 'FRIENDLY' ? '#22C55E' : status === 'NEUTRAL' ? '#3B82F6' : '#94A3B8'
  return L.divIcon({
    className: 'custom-drone-icon',
    html: `<div style="
      width: 28px; height: 28px;
      background: ${color}22;
      border: 2px solid ${color};
      border-radius: 50%;
      display: flex; align-items: center; justify-content: center;
      box-shadow: 0 0 8px ${color}44;
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

  // Filter entities with valid positions
  const droneMarkers = drones.filter((d: DroneRow) => d.lat !== 0 && d.lon !== 0)
  const nodeMarkers = nodes.filter((n: NodeRow) => n.lat && n.lon && (n.lat !== 0 || n.lon !== 0))

  // All positions for auto-fit
  const allPositions: [number, number][] = useMemo(() => {
    const pts: [number, number][] = []
    droneMarkers.forEach((d: DroneRow) => pts.push([d.lat, d.lon]))
    nodeMarkers.forEach((n: NodeRow) => {
      if (n.lat && n.lon) pts.push([n.lat, n.lon])
    })
    return pts
  }, [droneMarkers.length, nodeMarkers.length])

  const defaultCenter: [number, number] = [47.3769, 8.5417] // Zurich fallback
  const defaultZoom = 5

  return (
    <div className="h-full flex flex-col">
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
            <span className="inline-block w-3 h-3 rounded-full bg-primary-500" />
            <span className="text-dark-400">Nodes ({nodeMarkers.length})</span>
          </div>
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
          <TileLayer
            url="https://cartodb-basemaps-{s}.global.ssl.fastly.net/dark_all/{z}/{x}/{y}.png"
            subdomains="abcd"
            maxZoom={19}
          />

          <FitBounds positions={allPositions} />

          {/* Node markers (blue circles) */}
          {nodeMarkers.map((n: NodeRow) => (
            <CircleMarker
              key={`node-${n.id}`}
              center={[n.lat!, n.lon!]}
              radius={8}
              pathOptions={{
                fillColor: n.isOnline ? '#3B82F6' : '#475569',
                fillOpacity: n.isOnline ? 0.7 : 0.4,
                color: n.isOnline ? '#60A5FA' : '#64748B',
                weight: 2,
              }}
            >
              <Popup>
                <div className="text-xs min-w-[180px]" style={{ color: '#e2e8f0' }}>
                  <div style={{ fontWeight: 600, fontSize: '13px', marginBottom: '4px', color: '#f1f5f9' }}>
                    {n.name || n.shortName || `Node ${n.nodeNum}`}
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
          ))}

          {/* Drone markers (red icons) */}
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
                      color: d.status === 'HOSTILE' ? '#EF4444' :
                             d.status === 'FRIENDLY' ? '#22C55E' :
                             d.status === 'NEUTRAL' ? '#3B82F6' : '#94A3B8'
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
                    {d.source && <div>Source: {d.source}</div>}
                    {d.siteName && <div>Site: {d.siteName}</div>}
                    {d.lastSeen && <div style={{ marginTop: '2px', fontSize: '10px', color: '#64748B' }}>Last: {new Date(d.lastSeen).toLocaleString()}</div>}
                  </div>
                </div>
              </Popup>
            </Marker>
          ))}
        </MapContainer>

        {/* Overlay stats when no markers */}
        {droneMarkers.length === 0 && nodeMarkers.length === 0 && (
          <div className="absolute inset-0 flex items-center justify-center pointer-events-none z-[400]">
            <div className="bg-surface/90 backdrop-blur-sm rounded-xl border border-dark-700/50 px-6 py-4 text-center">
              <p className="text-dark-400 text-sm">No positioned entities</p>
              <p className="text-dark-500 text-xs mt-1">Drones and nodes with GPS data will appear here</p>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
