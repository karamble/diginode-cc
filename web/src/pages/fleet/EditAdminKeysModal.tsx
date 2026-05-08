// EditAdminKeysModal lets the operator pick which registered identities
// should appear in a target node's admin_key list. Maximum 3 entries
// (Meshtastic SecurityConfig.admin_key has 3 slots).
//
// Lockout-prevention: if the resulting list shares zero entries with
// the operator's identity registry, the backend refuses unless the
// operator types LOCKOUT and submits with force=true. We surface the
// warning client-side so the operator can decide before the round-trip.

import { useMutation, useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'

import fleetSecurityApi, {
  type IdentityRecord,
  type NodeTrust,
} from '../../api/fleetSecurity'
import PubkeyChip from '../../components/PubkeyChip'
import TypedConfirm, { confirmed } from '../../components/TypedConfirm'
import { ModalShell, ModalActions } from './IdentityImportModal'

interface Props {
  node: NodeTrust
  onClose: () => void
}

const MAX_KEYS = 3

export default function EditAdminKeysModal({ node, onClose }: Props) {
  const { data: registry, isLoading: regLoading } = useQuery<IdentityRecord[]>({
    queryKey: ['fleet-security', 'identities'],
    queryFn: () => fleetSecurityApi.listIdentities(),
  })

  // Filter out revoked identities from the picker.
  const candidates = useMemo(
    () => (registry ?? []).filter((r) => r.role !== 'revoked'),
    [registry],
  )

  // Pre-select whatever the node currently has (limited to known identities).
  const [selected, setSelected] = useState<Set<string>>(
    () => new Set(node.adminKeyFingerprints),
  )
  const [ack, setAck] = useState('')

  // Resulting list as an array (preserves selection order... ish).
  const result = useMemo(() => Array.from(selected), [selected])

  // Lockout-warning: would the resulting list contain zero registry entries?
  // We treat any selected fingerprint whose label exists in the registry as
  // "known" -- raw-pubkey entries on the node that we don't recognise stay
  // in adminKeyFingerprints but won't appear in candidates.
  const knownFps = new Set(candidates.map((c) => c.fingerprint))
  const knownAfter = result.filter((fp) => knownFps.has(fp))
  const wouldLockOut = knownAfter.length === 0

  const m = useMutation({
    mutationFn: () =>
      fleetSecurityApi.setAdminKeys(node.nodeNum, result, {
        force: wouldLockOut,
        ack: wouldLockOut ? ack : '',
      }),
    onSuccess: () => onClose(),
  })

  const toggle = (fp: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(fp)) {
        next.delete(fp)
      } else if (next.size < MAX_KEYS) {
        next.add(fp)
      }
      return next
    })
  }

  const canSubmit =
    !m.isPending &&
    result.length > 0 &&
    (!wouldLockOut || confirmed('LOCKOUT', ack))

  return (
    <ModalShell
      title={`Edit admin keys — ${node.longName || node.shortName || node.nodeNum.toString()}`}
      onClose={onClose}
    >
      <p className="text-xs text-dark-400 mb-3">
        Select which registered identities should appear in this node's
        admin_key list. Maximum {MAX_KEYS} entries.
      </p>

      {regLoading && <div className="text-sm text-dark-400">Loading registry…</div>}

      {candidates.length === 0 && !regLoading && (
        <div className="text-xs text-amber-300">
          No identities registered yet. Add at least one via "Manage identity
          registry…" before editing admin keys.
        </div>
      )}

      {candidates.length > 0 && (
        <div className="space-y-1.5 max-h-64 overflow-y-auto">
          {candidates.map((c) => {
            const isSelected = selected.has(c.fingerprint)
            const disabled = !isSelected && selected.size >= MAX_KEYS
            return (
              <label
                key={c.id}
                className={`flex items-center gap-3 p-2 rounded border transition-colors ${
                  isSelected
                    ? 'bg-primary-900/20 border-primary-700/40'
                    : 'bg-dark-900/40 border-dark-700/50 hover:border-dark-600'
                } ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}
              >
                <input
                  type="checkbox"
                  checked={isSelected}
                  disabled={disabled}
                  onChange={() => toggle(c.fingerprint)}
                  className="form-checkbox bg-dark-900 border-dark-600 text-primary-500 rounded focus:ring-0"
                />
                <PubkeyChip
                  fingerprint={c.fingerprint}
                  label={c.label}
                  role={c.role}
                  compact
                />
              </label>
            )
          })}
        </div>
      )}

      {wouldLockOut && (
        <div className="mt-4 bg-red-900/30 border border-red-700/40 rounded p-3 space-y-2">
          <div className="text-xs text-red-200 font-semibold">
            ⚠ Lockout warning
          </div>
          <p className="text-[11px] text-red-200/80">
            The resulting admin_key list contains no pubkey from your identity
            registry. After this push, no operator on this control-center will
            be able to admin this node remotely. Recovery would require physical
            USB access.
          </p>
          <TypedConfirm
            phrase="LOCKOUT"
            value={ack}
            onChange={setAck}
            hint="Type LOCKOUT to acknowledge"
          />
        </div>
      )}

      {m.error && (
        <div className="mt-3 text-xs text-red-300">
          {(m.error as Error).message}
        </div>
      )}

      <ModalActions
        onCancel={onClose}
        onSubmit={() => m.mutate()}
        submitLabel={`Push ${result.length} key${result.length === 1 ? '' : 's'}`}
        submitDisabled={!canSubmit}
        pending={m.isPending}
        destructive={wouldLockOut}
      />
    </ModalShell>
  )
}
