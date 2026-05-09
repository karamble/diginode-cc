// IdentityImportModal pushes a BYO X25519 keypair to the local Heltec
// and registers the pubkey as the new primary in the registry.
//
// Use cases:
//   - First-time setup with a deterministically-generated keypair.
//   - Disaster recovery: flash a fresh control-center Heltec with a
//     previously-escrowed keypair to resume admin without touching
//     deployed nodes.
//
// The privkey field is a textarea so operators can paste either raw
// 44-char base64 or pretty-formatted-with-newlines exports. Backend
// rejects anything that isn't 32 bytes after decode.

import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'

import fleetSecurityApi from '../../api/fleetSecurity'

interface Props {
  onClose: () => void
}

export default function IdentityImportModal({ onClose }: Props) {
  const [label, setLabel] = useState('')
  const [privB64, setPrivB64] = useState('')
  const [pubB64, setPubB64] = useState('')

  const m = useMutation({
    mutationFn: () =>
      fleetSecurityApi.importIdentity(
        label.trim(),
        privB64.trim(),
        pubB64.trim(),
      ),
    onSuccess: () => onClose(),
  })

  const canSubmit =
    label.trim().length > 0 && privB64.trim().length > 0 && pubB64.trim().length > 0

  return (
    <ModalShell onClose={onClose} title="Import keypair">
      <p className="text-xs text-dark-400 mb-4">
        Pushes the keypair to the local Heltec and registers the pubkey as
        the new primary control-center identity. The previous identity (if
        any) is no longer used to sign new admin transactions, but stays in
        the registry until you revoke it.
      </p>

      <div className="bg-amber-900/20 border border-amber-700/40 rounded p-3 mb-4">
        <div className="text-xs text-amber-200 font-semibold mb-1">
          Importing changes the local Heltec's identity
        </div>
        <ul className="text-[11px] text-amber-200/80 list-disc pl-4 space-y-0.5">
          <li>Phone clients bonded to the previous pubkey will need to re-pair.</li>
          <li>Other mesh nodes' NodeDB caches will be stale until they hear a fresh NodeInfo.</li>
          <li>The privkey bytes are sent to the local Heltec only (no PKC) and zeroed in memory after the request.</li>
        </ul>
      </div>

      <div className="space-y-3">
        <Field label="Label">
          <input
            type="text"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="e.g. cc-primary-2026Q2"
            className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-sm text-dark-200 focus:border-primary-500 focus:outline-none"
          />
        </Field>
        <Field label="Public key (base64, 44 chars)">
          <textarea
            value={pubB64}
            onChange={(e) => setPubB64(e.target.value)}
            rows={2}
            spellCheck={false}
            className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-xs font-mono text-dark-200 focus:border-primary-500 focus:outline-none"
          />
        </Field>
        <Field label="Private key (base64, 44 chars)">
          <textarea
            value={privB64}
            onChange={(e) => setPrivB64(e.target.value)}
            rows={2}
            spellCheck={false}
            className="w-full px-3 py-1.5 rounded bg-dark-900 border border-dark-600 text-xs font-mono text-dark-200 focus:border-primary-500 focus:outline-none"
          />
          <p className="mt-1 text-[10px] text-dark-500">
            Never logged. Cleared from memory after push.
          </p>
        </Field>
      </div>

      {m.error && (
        <div className="mt-3 text-xs text-red-300">
          {(m.error as Error).message}
        </div>
      )}

      <ModalActions
        onCancel={onClose}
        onSubmit={() => m.mutate()}
        submitLabel="Import"
        submitDisabled={!canSubmit || m.isPending}
        pending={m.isPending}
      />
    </ModalShell>
  )
}

// ---- Local UI helpers (kept here to avoid a tiny utils file) ----

interface ModalShellProps {
  title: string
  onClose: () => void
  children: React.ReactNode
}

export function ModalShell({ title, onClose, children }: ModalShellProps) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onClick={onClose}
    >
      <div
        className="bg-dark-800 border border-dark-700 rounded-lg shadow-xl w-full max-w-lg p-5 max-h-[90vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-4">
          <h4 className="text-sm font-semibold text-dark-100">{title}</h4>
          <button
            type="button"
            onClick={onClose}
            className="text-dark-500 hover:text-dark-200 text-lg leading-none"
            aria-label="Close"
          >
            ×
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}

interface FieldProps {
  label: string
  children: React.ReactNode
}

export function Field({ label, children }: FieldProps) {
  return (
    <div>
      <label className="block text-xs text-dark-300 mb-1">{label}</label>
      {children}
    </div>
  )
}

interface ModalActionsProps {
  onCancel: () => void
  onSubmit: () => void
  submitLabel: string
  submitDisabled?: boolean
  pending?: boolean
  destructive?: boolean
}

export function ModalActions({
  onCancel,
  onSubmit,
  submitLabel,
  submitDisabled,
  pending,
  destructive,
}: ModalActionsProps) {
  return (
    <div className="mt-5 flex items-center justify-end gap-2">
      <button
        type="button"
        onClick={onCancel}
        className="px-3 py-1.5 rounded bg-dark-700 hover:bg-dark-600 text-xs text-dark-200"
      >
        Cancel
      </button>
      <button
        type="button"
        onClick={onSubmit}
        disabled={submitDisabled}
        className={`px-3 py-1.5 rounded text-xs text-white transition-colors ${
          destructive
            ? 'bg-red-600 hover:bg-red-500 disabled:bg-red-900 disabled:text-red-300/60'
            : 'bg-primary-600 hover:bg-primary-500 disabled:bg-dark-700 disabled:text-dark-500'
        }`}
      >
        {pending ? 'Working…' : submitLabel}
      </button>
    </div>
  )
}
