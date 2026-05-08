// PubkeyChip renders a fingerprint identifier as a copyable chip,
// optionally with a human-readable label. The same component is used
// for pubkey fingerprints and PSK fingerprints (same colon-separated
// hex format on both, see internal/fleetsec/keys.go Fingerprint).
//
// Click → copies the fingerprint to clipboard (the underlying bytes
// are public; safe to paste anywhere).

import { useState } from 'react'

interface PubkeyChipProps {
  fingerprint: string
  label?: string
  // Optional role badge; one of 'primary' | 'rescue' | 'operator' | 'revoked'.
  // When 'revoked', the chip dims and shows a strike-through.
  role?: 'primary' | 'rescue' | 'operator' | 'revoked'
  // Compact omits the role badge and shrinks padding -- used inside
  // tight table cells.
  compact?: boolean
}

const roleColors: Record<string, string> = {
  primary: 'bg-primary-600/20 text-primary-300 border-primary-600/40',
  rescue: 'bg-amber-600/20 text-amber-300 border-amber-600/40',
  operator: 'bg-emerald-600/20 text-emerald-300 border-emerald-600/40',
  revoked: 'bg-dark-700/40 text-dark-500 border-dark-600/40',
}

export default function PubkeyChip({
  fingerprint,
  label,
  role,
  compact = false,
}: PubkeyChipProps) {
  const [copied, setCopied] = useState(false)

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation()
    // navigator.clipboard.writeText requires a secure context (HTTPS
    // or localhost). On plain-http LAN the secure context check fails
    // and the clipboard call silently rejects. Try the API first; fall
    // back to the legacy execCommand path which still works on http://.
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(fingerprint)
      } else {
        const ta = document.createElement('textarea')
        ta.value = fingerprint
        ta.style.position = 'fixed'
        ta.style.opacity = '0'
        document.body.appendChild(ta)
        ta.select()
        document.execCommand('copy')
        document.body.removeChild(ta)
      }
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // Both paths failed; leave silent so the chip doesn't grow noise.
    }
  }

  const isRevoked = role === 'revoked'
  const padding = compact ? 'px-2 py-0.5' : 'px-2.5 py-1'
  const text = compact ? 'text-[10px]' : 'text-xs'

  return (
    <button
      type="button"
      onClick={handleCopy}
      title={copied ? 'Copied!' : `Click to copy ${fingerprint}`}
      className={`inline-flex items-center gap-1.5 rounded border ${padding} ${text} font-mono whitespace-nowrap transition-colors ${
        roleColors[role ?? ''] ?? 'bg-dark-700/40 text-dark-300 border-dark-600/40'
      } hover:border-primary-500/60 ${isRevoked ? 'opacity-60 line-through' : ''}`}
    >
      {label && (
        <span className="font-sans font-medium not-italic">
          {label}
        </span>
      )}
      <span className="text-dark-400">{fingerprint.slice(0, 11)}</span>
      {!compact && <CopyIcon copied={copied} />}
    </button>
  )
}

function CopyIcon({ copied }: { copied: boolean }) {
  if (copied) {
    return (
      <svg className="w-3 h-3 text-green-400" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
        <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
      </svg>
    )
  }
  return (
    <svg className="w-3 h-3 text-dark-400" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
      <path strokeLinecap="round" strokeLinejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
    </svg>
  )
}
