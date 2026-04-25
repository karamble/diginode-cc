import { useEffect } from 'react'
import { LayersControl, TileLayer, useMap } from 'react-leaflet'
import L from 'leaflet'
import { useTileProvider, type TileProvider } from '../hooks/useTileProvider'

const PROVIDER_NAMES: Record<TileProvider, string> = {
  default: 'Default (OpenStreetMap)',
  matrix: 'Matrix (JAWG)',
}

const MATRIX_DISABLED_NAME = 'Matrix (JAWG — set JAWG_ACCESS_TOKEN)'

function nameToProvider(name: string): TileProvider {
  return name.startsWith('Matrix') ? 'matrix' : 'default'
}

// PersistOnLayerChange wires the Leaflet baselayerchange event to the
// useTileProvider hook so picking a layer in any map's LayersControl writes
// the choice back to /api/config (Postgres app_config table).
// When the JAWG token is missing and the user somehow lands on the disabled
// entry, we snap back to the default layer.
function PersistOnLayerChange() {
  const { setProvider, jawgAvailable } = useTileProvider()
  const map = useMap()

  useEffect(() => {
    const handler = (e: L.LayersControlEvent) => {
      const next = nameToProvider(e.name)
      if (next === 'matrix' && !jawgAvailable) return
      setProvider(next)
    }
    map.on('baselayerchange', handler)
    return () => {
      map.off('baselayerchange', handler)
    }
  }, [map, setProvider, jawgAvailable])

  return null
}

// TileLayerControl renders the persistent base-layer switcher. Place it as a
// child of <MapContainer>; it provides its own <LayersControl> wrapper. If a
// page also needs overlays, pass them as children — they're forwarded into the
// same LayersControl alongside the base layers.
export function TileLayerControl({
  position = 'topright',
  children,
}: {
  position?: 'topleft' | 'topright' | 'bottomleft' | 'bottomright'
  children?: React.ReactNode
}) {
  const { provider, jawgAvailable } = useTileProvider()
  const matrixName = jawgAvailable ? PROVIDER_NAMES.matrix : MATRIX_DISABLED_NAME

  return (
    <>
      <LayersControl position={position}>
        <LayersControl.BaseLayer
          checked={provider === 'default' || !jawgAvailable}
          name={PROVIDER_NAMES.default}
        >
          <TileLayer
            url="/api/tiles/osm/{z}/{x}/{y}"
            maxZoom={19}
            attribution="&copy; OpenStreetMap"
          />
        </LayersControl.BaseLayer>
        <LayersControl.BaseLayer
          checked={provider === 'matrix' && jawgAvailable}
          name={matrixName}
        >
          <TileLayer
            url="/api/tiles/jawg/{z}/{x}/{y}"
            maxZoom={22}
            attribution="&copy; Jawg Maps &copy; OpenStreetMap"
          />
        </LayersControl.BaseLayer>
        {children}
      </LayersControl>
      <PersistOnLayerChange />
    </>
  )
}

export default TileLayerControl
