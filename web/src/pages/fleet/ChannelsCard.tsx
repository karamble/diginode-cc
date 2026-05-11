// ChannelsCard renders one panel per known channel index. Shows PSK
// fingerprint, age, and last-rotated metadata; admin clicks Rotate PSK…
// to launch a fleet-wide rotation.
//
// Channels appear here once they've been seen via a previous rotation
// (or once a planned future "Reconcile drift" action populates the
// fleet_channels table). On a fresh install the table is empty -- the
// card surfaces guidance pointing at the first rotation flow.

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { QRCodeSVG } from 'qrcode.react'

import fleetSecurityApi, { type Channel, type ChannelReveal, type Rotation } from '../../api/fleetSecurity'
import { useAuthStore } from '../../stores/authStore'
import RotatePSKModal from './RotatePSKModal'
import RotationProgressDrawer from './RotationProgressDrawer'
import RetireOldPSKModal from './RetireOldPSKModal'

// copyToClipboard mirrors PubkeyChip / IdentityCard: try the secure-
// context clipboard API first, fall back to the legacy textarea +
// execCommand path so plain-http LAN deployments still get copy
// support. Returns true on success so the caller can flash a tick.
async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

export default function ChannelsCard() {
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'ADMIN'
  const qc = useQueryClient()

  const { data: channels, isLoading, error } = useQuery<Channel[]>({
    queryKey: ['fleet-security', 'channels'],
    queryFn: () => fleetSecurityApi.listChannels(),
  })

  const [rotating, setRotating] = useState<Channel | null>(null)
  const [activeRotation, setActiveRotation] = useState<{ id: string; pskB64: string } | null>(null)
  const [retireRotation, setRetireRotation] = useState<Rotation | null>(null)

  // Most-recent rotation per channel index, for the "Retire old PSK"
  // affordance. The card surfaces a Retire button when the rotation is
  // in the operator-paced retire window:
  //   3-phase atomic design: pi_local_phase = staging_added (Phase A
  //     complete, every reachable remote migrated atomically, Pi still
  //     on old PRIMARY waiting for operator-paced Phase C).
  //   legacy 5-phase design: pi_local_phase = phase_d_promoted.
  // Either way: not yet retired and ready for the Pi-side promote.
  const { data: pendingRetirements } = useQuery<Record<number, Rotation>>({
    queryKey: ['fleet-security', 'pending-retirements'],
    queryFn: async () => {
      const list = await fleetSecurityApi.listChannels()
      const out: Record<number, Rotation> = {}
      for (const ch of list) {
        if (!ch.lastRotationId) continue
        try {
          const rot = await fleetSecurityApi.getRotation(ch.lastRotationId)
          const ready =
            rot.piLocalPhase === 'staging_added' ||
            rot.piLocalPhase === 'phase_d_promoted'
          if (ready && !rot.retiredAt) {
            out[ch.index] = rot
          }
        } catch {
          // ignore -- rotation may have been pruned
        }
      }
      return out
    },
    refetchInterval: 30000,
  })

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
              pendingRetirement={pendingRetirements?.[ch.index]}
              onRetire={() => {
                const r = pendingRetirements?.[ch.index]
                if (r) setRetireRotation(r)
              }}
              onReopenStatus={() => {
                const r = pendingRetirements?.[ch.index]
                if (r) setActiveRotation({ id: r.id, pskB64: '' })
              }}
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
      {retireRotation && (
        <RetireOldPSKModal
          rotation={retireRotation}
          onClose={() => setRetireRotation(null)}
          onRetired={() => {
            setRetireRotation(null)
            // Server stamped retired_at + pi_local_phase=retired. Force
            // refetch of pending-retirements (drives the Retire button
            // visibility) and channels (so the card re-renders without
            // the migration progress strip). Without the explicit
            // invalidate the button hangs around for up to the
            // refetchInterval (30s) and a re-click hits "rotation
            // already retired".
            qc.invalidateQueries({ queryKey: ['fleet-security', 'pending-retirements'] })
            qc.invalidateQueries({ queryKey: ['fleet-security', 'channels'] })
            qc.invalidateQueries({ queryKey: ['fleet-security', 'trust'] })
          }}
        />
      )}
    </section>
  )
}

interface ChannelPanelProps {
  channel: Channel
  isAdmin: boolean
  onRotate: () => void
  // pendingRetirement is the most-recent rotation on this channel that
  // has finished Phase D (pi_local_phase = phase_d_promoted) and hasn't
  // been retired yet. Drives the migration progress strip + the Retire
  // button. undefined means no rotation is pending retirement on this
  // channel.
  pendingRetirement?: Rotation
  onRetire: () => void
  // onReopenStatus reopens the rotation progress drawer for this
  // channel's pending rotation. Lets operators get back to the Retry
  // button after they've closed the modal but the rotation still has
  // failed targets that need to catch up.
  onReopenStatus: () => void
}

function ChannelPanel({ channel, isAdmin, onRotate, pendingRetirement, onRetire, onReopenStatus }: ChannelPanelProps) {
  const ageStr = channel.lastRotatedAt
    ? formatRelative(channel.lastRotatedAt)
    : '—'

  const [revealed, setRevealed] = useState<ChannelReveal | null>(null)
  const revealMutation = useMutation({
    mutationFn: () => fleetSecurityApi.revealChannel(channel.index),
    onSuccess: (res) => setRevealed(res),
  })

  const retireSummary = (() => {
    if (!pendingRetirement) return null
    const targets = pendingRetirement.targets ?? []
    const total = targets.length
    const onNew = targets.filter((t) => t.phase === 'on_new_psk' || t.phase === 'retired').length
    return { total, onNew, gateOpen: total > 0 && onNew === total }
  })()

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
        <div className="flex items-center gap-2">
          {isAdmin && channel.role === 'PRIMARY' && (
            <button
              type="button"
              onClick={() => revealMutation.mutate()}
              disabled={revealMutation.isPending}
              title="Show the raw PSK + meshtastic enrollment URL/QR. Probes the live radio over 8 channel slots; takes a few seconds."
              className="px-3 py-1 rounded bg-dark-700 hover:bg-dark-600 disabled:bg-dark-800 disabled:text-dark-500 text-xs text-dark-200"
            >
              {revealMutation.isPending ? 'Reading…' : 'Reveal enrollment PSK'}
            </button>
          )}
          {isAdmin && pendingRetirement && (
            <button
              type="button"
              onClick={onRetire}
              disabled={!retireSummary?.gateOpen}
              title={retireSummary?.gateOpen
                ? 'All fleet members confirmed on the new PSK; safe to retire'
                : `Waiting on ${retireSummary ? retireSummary.total - retireSummary.onNew : '?'} laggards to migrate before retirement is safe`}
              className="px-3 py-1 rounded bg-amber-700/30 border border-amber-600/40 hover:bg-amber-700/50 disabled:bg-dark-700 disabled:border-dark-600 disabled:text-dark-500 text-xs text-amber-100"
            >
              Retire old PSK
              {retireSummary && (
                <span className="ml-1.5 text-[10px] opacity-70">
                  {retireSummary.onNew}/{retireSummary.total}
                </span>
              )}
            </button>
          )}
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
      </div>

      {revealMutation.isError && (
        <div className="mb-3 rounded border border-red-700/40 bg-red-900/20 px-3 py-2 text-[11px] text-red-200">
          {(revealMutation.error as Error).message}
        </div>
      )}

      {revealed && (
        <RevealBlock reveal={revealed} onClose={() => setRevealed(null)} />
      )}

      {pendingRetirement && retireSummary && (
        <div className="mb-3 rounded border border-amber-700/30 bg-amber-900/10 px-3 py-2 text-[11px] text-amber-100/90">
          <div className="flex items-center justify-between gap-3">
            <span>
              Migration in progress -- both PSKs alive on Pi
              {retireSummary.gateOpen
                ? ' (ready to retire)'
                : ` (${retireSummary.total - retireSummary.onNew} node${retireSummary.total - retireSummary.onNew === 1 ? '' : 's'} still on old PSK)`}
            </span>
            <div className="flex items-center gap-2 shrink-0">
              <span className="font-mono text-[10px] opacity-70">
                {retireSummary.onNew}/{retireSummary.total} on new
              </span>
              <button
                type="button"
                onClick={onReopenStatus}
                title="Reopen the rotation status drawer to see failed targets and retry"
                className="px-2 py-0.5 rounded border border-amber-600/40 bg-amber-700/20 hover:bg-amber-700/40 text-[10px] text-amber-100"
              >
                Open status
              </button>
            </div>
          </div>
        </div>
      )}

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

interface RevealBlockProps {
  reveal: ChannelReveal
  onClose: () => void
}

// RevealBlock surfaces the live primary-channel PSK material returned by
// /fleet-security/channels/:idx/reveal-psk. Three sections:
//   1. Raw base64 PSK -- pasted into flash-script prompts or
//      `meshtastic --ch-set psk base64:<value>`.
//   2. Enrollment QR -- scanned by the Meshtastic phone app to import
//      the full channel set (PRIMARY + any SECONDARY rotation slots).
//   3. Channel URL -- the same payload as the QR, copyable text for
//      `meshtastic --seturl '<url>'`. Collapsed by default so the QR
//      stays the visual focus.
function RevealBlock({ reveal, onClose }: RevealBlockProps) {
  const [pskCopied, setPskCopied] = useState(false)
  const [urlCopied, setUrlCopied] = useState(false)
  const [showUrl, setShowUrl] = useState(false)

  const handleCopy = async (text: string, setter: (v: boolean) => void) => {
    const ok = await copyToClipboard(text)
    if (ok) {
      setter(true)
      setTimeout(() => setter(false), 1500)
    }
  }

  return (
    <div className="mb-3 bg-dark-900/60 border border-dark-700/50 rounded p-3 space-y-3">
      <div className="flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-wider text-dark-300">
          Enrollment material for channel {reveal.index}
          {reveal.name && <span className="ml-1 normal-case text-dark-400">("{reveal.name}")</span>}
        </span>
        <button
          type="button"
          onClick={onClose}
          className="text-[10px] text-dark-500 hover:text-dark-300"
        >
          hide
        </button>
      </div>

      <div>
        <div className="flex items-center justify-between mb-1">
          <span className="text-[10px] uppercase tracking-wider text-dark-400">
            Primary PSK (base64) — paste into flash-script prompts
          </span>
          <button
            type="button"
            onClick={() => handleCopy(reveal.pskB64, setPskCopied)}
            className={`text-[10px] ${pskCopied ? 'text-emerald-400' : 'text-primary-400 hover:text-primary-300'}`}
          >
            {pskCopied ? '✓ copied!' : 'copy'}
          </button>
        </div>
        <code
          onClick={(e) => {
            const range = document.createRange()
            range.selectNodeContents(e.currentTarget)
            const sel = window.getSelection()
            sel?.removeAllRanges()
            sel?.addRange(range)
          }}
          className="block text-xs text-dark-200 font-mono break-all cursor-text select-all"
          title="Click to select all, then Ctrl+C"
        >
          {reveal.pskB64}
        </code>
      </div>

      <div>
        <div className="text-[10px] uppercase tracking-wider text-dark-400 mb-1.5">
          Enrollment QR — scan with the Meshtastic phone app
        </div>
        <div className="inline-block bg-white p-2 rounded">
          <QRCodeSVG value={reveal.channelUrl} size={192} level="M" />
        </div>
        <div className="text-[10px] text-dark-500 mt-1">
          Or run on a freshly flashed node:{' '}
          <code className="text-dark-400">meshtastic --seturl '&lt;url&gt;'</code>
        </div>
      </div>

      <div>
        <button
          type="button"
          onClick={() => setShowUrl((v) => !v)}
          className="text-[10px] text-dark-400 hover:text-dark-200"
        >
          {showUrl ? '▾' : '▸'} Channel URL (text)
        </button>
        {showUrl && (
          <div className="mt-1.5">
            <div className="flex items-center justify-end mb-1">
              <button
                type="button"
                onClick={() => handleCopy(reveal.channelUrl, setUrlCopied)}
                className={`text-[10px] ${urlCopied ? 'text-emerald-400' : 'text-primary-400 hover:text-primary-300'}`}
              >
                {urlCopied ? '✓ copied!' : 'copy'}
              </button>
            </div>
            <code
              onClick={(e) => {
                const range = document.createRange()
                range.selectNodeContents(e.currentTarget)
                const sel = window.getSelection()
                sel?.removeAllRanges()
                sel?.addRange(range)
              }}
              className="block text-[10px] text-dark-300 font-mono break-all cursor-text select-all"
              title="Click to select all, then Ctrl+C"
            >
              {reveal.channelUrl}
            </code>
          </div>
        )}
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
