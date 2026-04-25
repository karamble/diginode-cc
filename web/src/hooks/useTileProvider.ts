import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from '../api/client'

export type TileProvider = 'default' | 'matrix'

const PROVIDER_TO_SLUG: Record<TileProvider, string> = {
  default: 'osm',
  matrix: 'jawg',
}

const PROVIDER_MAX_ZOOM: Record<TileProvider, number> = {
  default: 19,
  matrix: 22,
}

type ConfigMap = Record<string, unknown>
type TilesInfo = { jawgAvailable: boolean }

function normalizeProvider(value: unknown): TileProvider {
  return value === 'matrix' ? 'matrix' : 'default'
}

export function useTileProvider() {
  const queryClient = useQueryClient()

  const { data: config } = useQuery<ConfigMap>({
    queryKey: ['config'],
    queryFn: () => api.get('/config'),
    staleTime: 30_000,
  })

  const { data: tilesInfo } = useQuery<TilesInfo>({
    queryKey: ['tiles', 'info'],
    queryFn: () => api.get('/tiles/info'),
    staleTime: 60_000,
  })

  const provider = normalizeProvider(config?.mapTileProvider)
  const jawgAvailable = tilesInfo?.jawgAvailable ?? false

  const setProviderMutation = useMutation({
    mutationFn: (next: TileProvider) =>
      api.put('/config/mapTileProvider', { value: next }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['config'] })
    },
  })

  const setProvider = (next: TileProvider) => {
    if (next === provider) return
    if (next === 'matrix' && !jawgAvailable) return
    setProviderMutation.mutate(next)
  }

  const tileUrl = (p: TileProvider = provider) =>
    `/api/tiles/${PROVIDER_TO_SLUG[p]}/{z}/{x}/{y}`

  const maxZoom = (p: TileProvider = provider) => PROVIDER_MAX_ZOOM[p]

  return { provider, setProvider, jawgAvailable, tileUrl, maxZoom }
}
