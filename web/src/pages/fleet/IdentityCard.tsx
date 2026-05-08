// IdentityCard renders the active control-center identity at the top
// of the Fleet Security tab. Pulls /fleet-security/identity (which the
// backend resolves by reading the local Heltec's SecurityConfig and
// joining against the identity registry).
//
// Errors here are common during normal operation (Heltec rebooting,
// USB cable unplugged), so we render a soft warning rather than a
// scary red error.

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'

import fleetSecurityApi, { type Identity } from '../../api/fleetSecurity'
import { useAuthStore } from '../../stores/authStore'
import PubkeyChip from '../../components/PubkeyChip'
import IdentityImportModal from './IdentityImportModal'
import IdentityRegistryModal from './IdentityRegistryModal'

export default function IdentityCard() {
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'ADMIN'
  const queryClient = useQueryClient()

  const [importOpen, setImportOpen] = useState(false)
  const [registryOpen, setRegistryOpen] = useState(false)
  const [exported, setExported] = useState<string | null>(null)

  const { data, isLoading, error } = useQuery<Identity>({
    queryKey: ['fleet-security', 'identity'],
    queryFn: () => fleetSecurityApi.getIdentity(),
    retry: false, // 502 means no Heltec; retrying won't help
  })

  const exportMutation = useMutation({
    mutationFn: () => fleetSecurityApi.exportPubkey(),
    onSuccess: (res) => {
      setExported(res.publicKeyB64)
    },
  })

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['fleet-security', 'identity'] })
    queryClient.invalidateQueries({ queryKey: ['fleet-security', 'identities'] })
  }

  return (
    <section className="bg-dark-800/60 border border-dark-700/50 rounded-lg p-5">
      <header className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-sm font-semibold text-dark-100">
            Control Center Identity
          </h3>
          <p className="text-xs text-dark-400 mt-0.5">
            The X25519 keypair this control-center uses to sign remote-admin
            transactions to deployed nodes.
          </p>
        </div>
        <button
          type="button"
          onClick={refresh}
          className="text-xs text-dark-400 hover:text-dark-200 transition-colors"
          title="Re-read identity from local Heltec"
        >
          ↻ Refresh
        </button>
      </header>

      {isLoading && (
        <div className="text-sm text-dark-400">Loading identity…</div>
      )}

      {error && (
        <div className="bg-amber-900/20 border border-amber-700/40 rounded p-3 text-xs text-amber-200">
          <div className="font-semibold mb-1">Identity unavailable</div>
          <div className="text-amber-300/80 font-mono break-all">
            {String((error as Error).message)}
          </div>
          <div className="mt-2 text-amber-200/70">
            This usually means the local Heltec is disconnected or rebooting.
            Check the Serial card on the Config page.
          </div>
        </div>
      )}

      {data && (
        <div className="space-y-3">
          <div className="flex items-center gap-3 flex-wrap">
            <PubkeyChip
              label={data.label || '(unregistered)'}
              fingerprint={data.fingerprint}
              role={data.role === 'revoked' ? 'revoked' : 'primary'}
            />
            {data.source && (
              <span className="text-[10px] uppercase tracking-wider text-dark-400">
                source: {data.source}
              </span>
            )}
          </div>

          <div className="flex items-center gap-2 flex-wrap">
            <button
              type="button"
              onClick={() => exportMutation.mutate()}
              className="px-3 py-1.5 rounded bg-dark-700 hover:bg-dark-600 text-xs text-dark-200 transition-colors"
            >
              Export pubkey…
            </button>
            {isAdmin && (
              <>
                <button
                  type="button"
                  onClick={() => setImportOpen(true)}
                  className="px-3 py-1.5 rounded bg-dark-700 hover:bg-dark-600 text-xs text-dark-200 transition-colors"
                >
                  Import keypair…
                </button>
                <button
                  type="button"
                  onClick={() => setRegistryOpen(true)}
                  className="px-3 py-1.5 rounded bg-dark-700 hover:bg-dark-600 text-xs text-dark-200 transition-colors"
                >
                  Manage identity registry…
                </button>
              </>
            )}
          </div>

          {exported && (
            <div className="bg-dark-900/60 border border-dark-700/50 rounded p-3">
              <div className="flex items-center justify-between mb-1.5">
                <span className="text-[10px] uppercase tracking-wider text-dark-400">
                  Public key (base64)
                </span>
                <button
                  type="button"
                  onClick={() => navigator.clipboard.writeText(exported)}
                  className="text-[10px] text-primary-400 hover:text-primary-300"
                >
                  copy
                </button>
              </div>
              <code className="block text-xs text-dark-200 font-mono break-all">
                {exported}
              </code>
              <button
                type="button"
                onClick={() => setExported(null)}
                className="mt-2 text-[10px] text-dark-500 hover:text-dark-300"
              >
                close
              </button>
            </div>
          )}
        </div>
      )}

      {importOpen && (
        <IdentityImportModal
          onClose={() => {
            setImportOpen(false)
            refresh()
          }}
        />
      )}
      {registryOpen && (
        <IdentityRegistryModal
          onClose={() => {
            setRegistryOpen(false)
            refresh()
          }}
        />
      )}
    </section>
  )
}
