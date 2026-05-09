// IdentityRegistryModal lists all registered pubkeys (active + revoked)
// with role badges, and lets ADMIN add a new pubkey-only entry or
// revoke an existing one. The pubkey-only flow is for "Add operator"
// or "Add rescue key" -- the privkey lives outside diginode-cc.

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'

import fleetSecurityApi, {
  type IdentityRecord,
  type IdentityRole,
} from '../../api/fleetSecurity'
import { useAuthStore } from '../../stores/authStore'
import PubkeyChip from '../../components/PubkeyChip'
import { ModalShell, Field, ModalActions } from './IdentityImportModal'

interface Props {
  onClose: () => void
}

export default function IdentityRegistryModal({ onClose }: Props) {
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'ADMIN'
  const qc = useQueryClient()

  const { data, isLoading } = useQuery<IdentityRecord[]>({
    queryKey: ['fleet-security', 'identities'],
    queryFn: () => fleetSecurityApi.listIdentities(),
  })

  const refresh = () =>
    qc.invalidateQueries({ queryKey: ['fleet-security', 'identities'] })

  const [showAdd, setShowAdd] = useState(false)
  const [newLabel, setNewLabel] = useState('')
  const [newPub, setNewPub] = useState('')
  const [newRole, setNewRole] = useState<IdentityRole>('operator')

  const addM = useMutation({
    mutationFn: () =>
      fleetSecurityApi.registerIdentity(newLabel.trim(), newPub.trim(), newRole),
    onSuccess: () => {
      setShowAdd(false)
      setNewLabel('')
      setNewPub('')
      setNewRole('operator')
      refresh()
    },
  })

  const revokeM = useMutation({
    mutationFn: ({ fp, reason }: { fp: string; reason: string }) =>
      fleetSecurityApi.revokeIdentity(fp, reason),
    onSuccess: refresh,
  })

  return (
    <ModalShell title="Identity registry" onClose={onClose}>
      <p className="text-xs text-dark-400 mb-4">
        Named pubkeys this control-center trusts for fleet operations. Add
        operator or rescue keys here so they appear in admin-key dropdowns
        across the fleet. Revoked entries stay so audit log references
        remain resolvable to a label.
      </p>

      {isLoading && (
        <div className="text-sm text-dark-400">Loading…</div>
      )}

      {data && (
        <div className="space-y-2">
          {data.length === 0 && (
            <div className="text-xs text-dark-500 italic">
              Registry is empty.
            </div>
          )}
          {data.map((id) => (
            <div
              key={id.id}
              className="flex items-center justify-between bg-dark-900/40 border border-dark-700/50 rounded p-2"
            >
              <div className="flex items-center gap-3 min-w-0">
                <PubkeyChip
                  fingerprint={id.fingerprint}
                  label={id.label}
                  role={id.role}
                />
                <span className="text-[10px] text-dark-500">
                  {id.source}
                </span>
              </div>
              {isAdmin && id.role !== 'revoked' && (
                <button
                  type="button"
                  disabled={revokeM.isPending}
                  onClick={() => {
                    const reason = window.prompt(
                      `Reason for revoking "${id.label}"?`,
                      '',
                    )
                    if (reason === null) return
                    revokeM.mutate({ fp: id.fingerprint, reason })
                  }}
                  className="text-[10px] text-red-400 hover:text-red-300"
                >
                  Revoke
                </button>
              )}
            </div>
          ))}
        </div>
      )}

      {isAdmin && !showAdd && (
        <button
          type="button"
          onClick={() => setShowAdd(true)}
          className="mt-4 px-3 py-1.5 rounded bg-dark-700 hover:bg-dark-600 text-xs text-dark-200"
        >
          + Add identity
        </button>
      )}

      {isAdmin && showAdd && (
        <div className="mt-4 border-t border-dark-700/50 pt-4 space-y-3">
          <h5 className="text-xs font-semibold text-dark-200">
            Add a pubkey-only identity
          </h5>
          <p className="text-[10px] text-dark-500">
            Use this for operator or rescue keys whose privkey lives in
            cold storage (not on this control-center Heltec).
          </p>
          <Field label="Label">
            <input
              type="text"
              value={newLabel}
              onChange={(e) => setNewLabel(e.target.value)}
              placeholder="e.g. cc-rescue-cold"
              className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-sm text-dark-200 focus:border-primary-500 focus:outline-none"
            />
          </Field>
          <Field label="Public key (base64)">
            <textarea
              value={newPub}
              onChange={(e) => setNewPub(e.target.value)}
              rows={2}
              spellCheck={false}
              className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-xs font-mono text-dark-200 focus:border-primary-500 focus:outline-none"
            />
          </Field>
          <Field label="Role">
            <select
              value={newRole}
              onChange={(e) => setNewRole(e.target.value as IdentityRole)}
              className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-sm text-dark-200 focus:border-primary-500 focus:outline-none"
            >
              <option value="rescue">rescue (cold-storage recovery key)</option>
              <option value="operator">operator (additional admin)</option>
              <option value="primary">primary (active control-center)</option>
            </select>
          </Field>
          {addM.error && (
            <div className="text-xs text-red-300">
              {(addM.error as Error).message}
            </div>
          )}
          <div className="flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={() => setShowAdd(false)}
              className="text-xs text-dark-400 hover:text-dark-200"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => addM.mutate()}
              disabled={!newLabel.trim() || !newPub.trim() || addM.isPending}
              className="px-3 py-1.5 rounded bg-primary-600 hover:bg-primary-500 disabled:bg-dark-700 disabled:text-dark-500 text-xs text-white"
            >
              {addM.isPending ? 'Adding…' : 'Add'}
            </button>
          </div>
        </div>
      )}

      <ModalActions
        onCancel={onClose}
        onSubmit={onClose}
        submitLabel="Done"
      />
    </ModalShell>
  )
}
