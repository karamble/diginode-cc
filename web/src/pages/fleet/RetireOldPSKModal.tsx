// RetireOldPSKModal -- Phase E of the staged PSK rotation. Disables the
// old SECONDARY channel slot on the local Heltec and broadcasts the
// same SetChannel(role=DISABLED) to each fleet member that's still
// reachable.
//
// Gate: every managed-trust row's currentPskFp must equal the
// rotation's new fingerprint. The backend returns 409 with a laggards
// list on gate failure; the UI renders that list and disables the
// "Retire" button until the operator runs Verify on each laggard.
//
// Typed-confirmation: operator types "RETIRE" before the action button
// enables. Same pattern as the rotate / lockout flows.

import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'

import fleetSecurityApi, {
  type RetireOldPSKResult,
  type Rotation,
} from '../../api/fleetSecurity'
import { ModalShell, ModalActions, Field } from './IdentityImportModal'

interface Props {
  rotation: Rotation
  onClose: () => void
  onRetired: () => void
}

export default function RetireOldPSKModal({ rotation, onClose, onRetired }: Props) {
  const [ack, setAck] = useState('')
  const [laggards, setLaggards] = useState<number[] | null>(null)

  const retireM = useMutation<RetireOldPSKResult, Error>({
    mutationFn: () => fleetSecurityApi.retireOldPSK(rotation.id),
    onSuccess: (res) => {
      if (res.ok) {
        onRetired()
        onClose()
      } else {
        // Gate failed -- render the laggards list inline.
        setLaggards(res.laggards ?? [])
      }
    },
    onError: (err) => {
      // 409 with body comes through as an Error from the api client;
      // the body's parsed inside the mutation already, but a real
      // network error / 500 lands here.
      console.error('retireOldPSK error', err)
    },
  })

  const ready = ack === 'RETIRE' && !retireM.isPending
  const targets = rotation.targets ?? []
  const onNew = targets.filter((t) => t.phase === 'on_new_psk' || t.phase === 'retired').length
  const total = targets.length

  return (
    <ModalShell
      title={`Retire old PSK — rotation ${rotation.id.slice(0, 8)}`}
      onClose={onClose}
    >
      <div className="space-y-3">
        <div className="text-xs text-dark-300">
          Phase E disables the old SECONDARY channel slot on the local Heltec and
          (best-effort) on each fleet member that's still reachable. Channel
          {rotation.channelIndex ?? '?'} on the new fingerprint becomes the
          only PRIMARY. After this the old PSK is wiped and any node that
          hasn't migrated permanently loses comms with the fleet.
        </div>

        <div className="rounded border border-dark-700/50 px-3 py-2 text-xs">
          <div className="text-dark-200">
            <span className="font-mono">{onNew}/{total}</span> targets on the new PSK
          </div>
          <div className="text-[10px] text-dark-500 mt-1">
            New fingerprint: <span className="font-mono">{rotation.newPskFingerprint}</span>
          </div>
        </div>

        {laggards !== null && laggards.length > 0 && (
          <div className="rounded border border-amber-700/40 bg-amber-900/20 p-3">
            <div className="text-xs text-amber-200 font-semibold mb-1">
              Gate failed: {laggards.length} node{laggards.length === 1 ? '' : 's'} still on a stale PSK
            </div>
            <div className="space-y-0.5 font-mono text-[11px] text-amber-100/80">
              {laggards.map((n) => (
                <div key={n}>!{n.toString(16).padStart(8, '0')}</div>
              ))}
            </div>
            <div className="text-[10px] text-amber-200/70 mt-2">
              Run Verify on each laggard from the Trust roster. Once
              every node is on the new PSK the gate opens automatically.
            </div>
          </div>
        )}

        <Field label="Type RETIRE to confirm">
          <input
            type="text"
            value={ack}
            onChange={(e) => setAck(e.target.value.toUpperCase())}
            spellCheck={false}
            placeholder="RETIRE"
            className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-xs font-mono text-dark-200 focus:border-primary-500 focus:outline-none"
          />
          <p className="mt-1 text-[10px] text-dark-500">
            Required because retirement is irreversible -- the firmware wipes
            the PSK material from the disabled slot.
          </p>
        </Field>

        {retireM.error && (
          <div className="text-[11px] text-red-300">
            {retireM.error.message}
          </div>
        )}
      </div>

      <ModalActions
        onCancel={onClose}
        onSubmit={() => retireM.mutate()}
        submitLabel={retireM.isPending ? 'Retiring…' : 'Retire old PSK'}
        submitDisabled={!ready}
        pending={retireM.isPending}
        destructive
      />
    </ModalShell>
  )
}
