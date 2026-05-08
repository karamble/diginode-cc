// ChannelsCard renders one panel per known channel index. Shows PSK
// fingerprint, age, and last-rotated metadata; admin clicks Rotate PSK…
// to launch a fleet-wide rotation.
//
// Channels appear here once they've been seen via a previous rotation
// (or once a planned future "Reconcile drift" action populates the
// fleet_channels table). On a fresh install the table is empty -- the
// card surfaces guidance pointing at the first rotation flow.

import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import fleetSecurityApi, { type Channel } from '../../api/fleetSecurity'
import { useAuthStore } from '../../stores/authStore'
import RotatePSKModal from './RotatePSKModal'
import RotationProgressDrawer from './RotationProgressDrawer'

export default function ChannelsCard() {
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'ADMIN'

  const { data: channels, isLoading, error } = useQuery<Channel[]>({
    queryKey: ['fleet-security', 'channels'],
    queryFn: () => fleetSecurityApi.listChannels(),
  })

  const [rotating, setRotating] = useState<Channel | null>(null)
  const [activeRotation, setActiveRotation] = useState<{ id: string; pskB64: string } | null>(null)

  return (
    <section className="bg-dark-800/60 border border-dark-700/50 rounded-lg p-5">
      <header className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-sm font-semibold text-dark-100">Channels</h3>
          <p className="text-xs text-dark-400 mt-0.5">
            Per-channel PSK lifecycle. Rotation walks the fleet via PKC
            remote admin; the local Heltec rotates last so we don't lose
            mesh reach mid-flight.
          </p>
        </div>
      </header>

      {isLoading && <div className="text-sm text-dark-400">Loading channels…</div>}
      {error && (
        <div className="text-xs text-red-300">{(error as Error).message}</div>
      )}

      {channels && channels.length === 0 && (
        <div className="bg-dark-900/40 border border-dark-700/50 rounded p-4 space-y-3">
          <p className="text-xs text-dark-300">
            No channel snapshots yet. The fleet_channels table is populated
            after the first PSK rotation completes (the rotation runner
            writes the new fingerprint + last_rotated_at into the row).
          </p>
          {isAdmin && (
            <button
              type="button"
              onClick={() =>
                setRotating({
                  index: 0,
                  name: 'primary',
                  role: 'PRIMARY',
                  pskLength: 16,
                })
              }
              className="px-3 py-1.5 rounded bg-primary-600 hover:bg-primary-500 text-xs text-white"
            >
              Rotate channel 0 PSK…
            </button>
          )}
        </div>
      )}

      {channels && channels.length > 0 && (
        <div className="space-y-3">
          {channels.map((ch) => (
            <ChannelPanel
              key={ch.index}
              channel={ch}
              isAdmin={isAdmin}
              onRotate={() => setRotating(ch)}
            />
          ))}
        </div>
      )}

      {rotating && (
        <RotatePSKModal
          channel={rotating}
          onClose={() => setRotating(null)}
          onRotationStarted={(rotationId, pskB64) => {
            setRotating(null)
            setActiveRotation({ id: rotationId, pskB64 })
          }}
        />
      )}
      {activeRotation && (
        <RotationProgressDrawer
          rotationId={activeRotation.id}
          pskForRetry={activeRotation.pskB64}
          onClose={() => setActiveRotation(null)}
        />
      )}
    </section>
  )
}

interface ChannelPanelProps {
  channel: Channel
  isAdmin: boolean
  onRotate: () => void
}

function ChannelPanel({ channel, isAdmin, onRotate }: ChannelPanelProps) {
  const ageStr = channel.lastRotatedAt
    ? formatRelative(channel.lastRotatedAt)
    : '—'
  return (
    <div className="bg-dark-900/40 border border-dark-700/50 rounded p-4">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-3">
          <span className="text-sm font-semibold text-dark-100">
            Channel {channel.index}
          </span>
          {channel.name && (
            <span className="text-xs text-dark-300">"{channel.name}"</span>
          )}
          <span className="text-[10px] uppercase tracking-wider text-dark-400">
            {channel.role}
          </span>
        </div>
        {isAdmin && (
          <button
            type="button"
            onClick={onRotate}
            className="px-3 py-1 rounded bg-dark-700 hover:bg-dark-600 text-xs text-dark-200"
          >
            Rotate PSK…
          </button>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3 text-xs">
        <div>
          <div className="text-[10px] uppercase tracking-wider text-dark-500">
            PSK fingerprint
          </div>
          <div className="font-mono text-dark-200 mt-0.5">
            {channel.pskFingerprint || <span className="text-dark-500">—</span>}
          </div>
        </div>
        <div>
          <div className="text-[10px] uppercase tracking-wider text-dark-500">
            PSK length
          </div>
          <div className="text-dark-200 mt-0.5">
            {channel.pskLength
              ? `${channel.pskLength} bytes`
              : <span className="text-dark-500">unset</span>}
          </div>
        </div>
        <div>
          <div className="text-[10px] uppercase tracking-wider text-dark-500">
            Last rotated
          </div>
          <div className="text-dark-200 mt-0.5" title={channel.lastRotatedAt}>
            {ageStr}
          </div>
        </div>
        <div>
          <div className="text-[10px] uppercase tracking-wider text-dark-500">
            Last rotation
          </div>
          <div className="text-dark-200 mt-0.5 font-mono text-[10px]">
            {channel.lastRotationId
              ? channel.lastRotationId.slice(0, 8)
              : <span className="text-dark-500">—</span>}
          </div>
        </div>
      </div>
    </div>
  )
}

function formatRelative(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime()
  const sec = Math.floor(ms / 1000)
  if (sec < 60) return `${sec}s ago`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 48) return `${hr}h ago`
  return `${Math.floor(hr / 24)}d ago`
}
