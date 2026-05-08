// TrustHealthPill renders the per-node trust-roster status as a colored
// chip. Combines two backend signals:
//   - DriftStatus: in-policy / drift / unreachable / unknown
//   - lastVerifiedAt age: green ≤1h, yellow ≤24h, orange >24h
//
// Drift > age: a known-drifted node always pills as drift even if
// recently verified, because that's the more actionable signal.

import type { DriftStatus } from '../api/fleetSecurity'

interface TrustHealthPillProps {
  driftStatus: DriftStatus
  lastVerifiedAt?: string
}

interface PillStyle {
  cls: string
  label: string
  title: string
}

function pillFor(
  driftStatus: DriftStatus,
  lastVerifiedAt?: string,
): PillStyle {
  if (driftStatus === 'drift') {
    return {
      cls: 'bg-red-600/20 text-red-300 border-red-600/40',
      label: 'drift',
      title: 'Node admin_key list does not match fleet policy',
    }
  }
  if (driftStatus === 'unreachable') {
    return {
      cls: 'bg-red-600/20 text-red-300 border-red-600/40',
      label: 'unreachable',
      title: 'Last verify attempt failed -- node may be offline or out of range',
    }
  }
  if (!lastVerifiedAt) {
    return {
      cls: 'bg-dark-700/40 text-dark-400 border-dark-600/40',
      label: 'unverified',
      title: 'Trust state has never been verified for this node',
    }
  }

  const ageMs = Date.now() - new Date(lastVerifiedAt).getTime()
  const hours = ageMs / 3_600_000

  if (hours <= 1) {
    return {
      cls: 'bg-emerald-600/20 text-emerald-300 border-emerald-600/40',
      label: 'verified',
      title: `Verified ${formatAge(ageMs)} ago`,
    }
  }
  if (hours <= 24) {
    return {
      cls: 'bg-amber-600/20 text-amber-300 border-amber-600/40',
      label: 'stale',
      title: `Verified ${formatAge(ageMs)} ago -- consider re-verifying`,
    }
  }
  return {
    cls: 'bg-orange-600/20 text-orange-300 border-orange-600/40',
    label: 'old',
    title: `Verified ${formatAge(ageMs)} ago -- trust state may not reflect current node config`,
  }
}

function formatAge(ms: number): string {
  const sec = Math.floor(ms / 1000)
  if (sec < 60) return `${sec}s`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m`
  const hr = Math.floor(min / 60)
  if (hr < 48) return `${hr}h`
  const days = Math.floor(hr / 24)
  return `${days}d`
}

export default function TrustHealthPill({
  driftStatus,
  lastVerifiedAt,
}: TrustHealthPillProps) {
  const { cls, label, title } = pillFor(driftStatus, lastVerifiedAt)
  return (
    <span
      title={title}
      className={`inline-flex items-center px-2 py-0.5 rounded border text-[10px] font-medium uppercase tracking-wider ${cls}`}
    >
      {label}
    </span>
  )
}
