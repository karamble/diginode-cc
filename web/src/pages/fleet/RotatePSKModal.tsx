// RotatePSKModal collects new-PSK + targets + ROTATE confirmation, then
// kicks off a fleet-wide rotation. Hands off to RotationProgressDrawer
// (mounted as a sibling by ChannelsCard) once the backend accepts the
// kickoff.
//
// PSK source defaults to "random 16 bytes" -- explicit-base64 is for
// operators rotating to a pre-arranged value (rare).

import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import fleetSecurityApi, {
  type Channel,
  type NodeTrust,
} from '../../api/fleetSecurity'
import TypedConfirm, { confirmed } from '../../components/TypedConfirm'
import { ModalShell, ModalActions, Field } from './IdentityImportModal'

interface Props {
  channel: Channel
  onClose: () => void
  onRotationStarted: (rotationId: string, pskB64: string) => void
}

type Source = 'random' | 'explicit'

export default function RotatePSKModal({
  channel,
  onClose,
  onRotationStarted,
}: Props) {
  const { data: nodes } = useQuery<NodeTrust[]>({
    queryKey: ['fleet-security', 'trust'],
    queryFn: () => fleetSecurityApi.listTrust(),
  })

  const candidates = (nodes ?? []).filter((n) => n.nodeNum !== 0)
  // Default to all healthy (i.e. ones whose trust pill isn't unreachable).
  const defaultTargets = candidates
    .filter((n) => n.driftStatus !== 'unreachable')
    .map((n) => n.nodeNum)

  const [source, setSource] = useState<Source>('random')
  const [explicitB64, setExplicitB64] = useState('')
  const [selectedTargets, setSelectedTargets] = useState<Set<number>>(
    () => new Set(defaultTargets),
  )
  const [ack, setAck] = useState('')
  const [interTargetMs, setInterTargetMs] = useState(0)

  const m = useMutation({
    mutationFn: () => {
      // For random source we don't have the bytes locally -- the
      // backend generates them. The retry path then needs the operator
      // to re-paste; for now we emit empty pskB64 to the drawer so it
      // shows the paste box. For explicit source, we already have the
      // base64 to seed the retry box.
      const pskB64 = source === 'explicit' ? explicitB64.trim() : ''
      return fleetSecurityApi
        .rotatePSK(channel.index, {
          source,
          pskB64: source === 'explicit' ? explicitB64.trim() : undefined,
          targets: Array.from(selectedTargets),
          ack,
          interTargetDelayMs: interTargetMs,
        })
        .then((res) => ({ id: res.rotationId, pskB64 }))
    },
    onSuccess: ({ id, pskB64 }) => {
      onRotationStarted(id, pskB64)
    },
  })

  const toggle = (nodeNum: number) => {
    setSelectedTargets((prev) => {
      const next = new Set(prev)
      if (next.has(nodeNum)) next.delete(nodeNum)
      else next.add(nodeNum)
      return next
    })
  }

  const targetCount = selectedTargets.size
  const canSubmit =
    !m.isPending &&
    targetCount > 0 &&
    confirmed('ROTATE', ack) &&
    (source === 'random' || explicitB64.trim().length > 0)

  return (
    <ModalShell
      title={`Rotate PSK — channel ${channel.index} ${channel.name ? `(${channel.name})` : ''}`}
      onClose={onClose}
    >
      <p className="text-xs text-dark-400 mb-4">
        Pushes a new PSK to every selected node sequentially via remote
        admin (PKC), then to the local Heltec last so we don't lose mesh
        reach mid-rotation. Failed targets retain the old PSK and land in
        the retry tray.
      </p>

      <div className="bg-amber-900/20 border border-amber-700/40 rounded p-3 mb-4">
        <div className="text-xs text-amber-200 font-semibold mb-1">
          Forward secrecy reminder
        </div>
        <p className="text-[11px] text-amber-200/80">
          Anyone who logged ciphertext under the old PSK can still decrypt
          that ciphertext. Rotation limits future exposure, not past.
          Nodes offline during this rotation will be stranded on the old
          PSK until you re-push individually.
        </p>
      </div>

      <div className="space-y-4">
        <Field label="Source">
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-1.5 text-xs text-dark-200">
              <input
                type="radio"
                name="source"
                value="random"
                checked={source === 'random'}
                onChange={() => setSource('random')}
                className="form-radio bg-dark-900 border-dark-600 text-primary-500 focus:ring-0"
              />
              Random 16 bytes
            </label>
            <label className="flex items-center gap-1.5 text-xs text-dark-200">
              <input
                type="radio"
                name="source"
                value="explicit"
                checked={source === 'explicit'}
                onChange={() => setSource('explicit')}
                className="form-radio bg-dark-900 border-dark-600 text-primary-500 focus:ring-0"
              />
              Specify (base64, 32 bytes)
            </label>
          </div>
        </Field>

        {source === 'explicit' && (
          <Field label="PSK (base64)">
            <textarea
              value={explicitB64}
              onChange={(e) => setExplicitB64(e.target.value)}
              rows={2}
              spellCheck={false}
              className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-xs font-mono text-dark-200 focus:border-primary-500 focus:outline-none"
            />
          </Field>
        )}

        <Field label={`Targets (${targetCount} selected)`}>
          <div className="max-h-44 overflow-y-auto rounded border border-dark-700/50 divide-y divide-dark-700/30">
            {candidates.length === 0 && (
              <div className="px-3 py-2 text-[11px] text-dark-500 italic">
                No nodes in the roster.
              </div>
            )}
            {candidates.map((n) => (
              <label
                key={n.nodeNum}
                className="flex items-center gap-3 px-3 py-1.5 cursor-pointer hover:bg-dark-800/30"
              >
                <input
                  type="checkbox"
                  checked={selectedTargets.has(n.nodeNum)}
                  onChange={() => toggle(n.nodeNum)}
                  className="form-checkbox bg-dark-900 border-dark-600 text-primary-500 rounded focus:ring-0"
                />
                <span className="text-xs text-dark-100">
                  {n.shortName || n.longName || `node ${n.nodeNum}`}
                </span>
                {n.driftStatus === 'unreachable' && (
                  <span className="ml-auto text-[10px] text-red-300">
                    unreachable
                  </span>
                )}
              </label>
            ))}
          </div>
        </Field>

        <Field label="Inter-target delay (ms)">
          <input
            type="number"
            min={0}
            value={interTargetMs}
            onChange={(e) => setInterTargetMs(Math.max(0, parseInt(e.target.value || '0', 10)))}
            className="w-32 px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-sm text-dark-200 focus:border-primary-500 focus:outline-none"
          />
          <p className="mt-1 text-[10px] text-dark-500">
            Optional pacing for EU868 / similar duty-cycle limits. 0 = no extra delay.
          </p>
        </Field>

        <TypedConfirm
          phrase="ROTATE"
          value={ack}
          onChange={setAck}
        />
      </div>

      {m.error && (
        <div className="mt-3 text-xs text-red-300">
          {(m.error as Error).message}
        </div>
      )}

      <ModalActions
        onCancel={onClose}
        onSubmit={() => m.mutate()}
        submitLabel={`Rotate ${targetCount} target${targetCount === 1 ? '' : 's'}`}
        submitDisabled={!canSubmit}
        pending={m.isPending}
        destructive
      />
    </ModalShell>
  )
}
