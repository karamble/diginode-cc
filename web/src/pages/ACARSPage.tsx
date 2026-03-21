import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from '../api/client'

interface ACARSMessage {
  id?: string
  timestamp: string
  flight?: string
  text?: string
  label?: string
  blockId?: string
  msgNum?: string
  registrationNo?: string
  frequency?: number
  level?: number
}

interface ACARSStatus {
  enabled: boolean
  status: string
  messageCount: number
}

export default function ACARSPage() {
  const queryClient = useQueryClient()

  const { data: status } = useQuery<ACARSStatus>({
    queryKey: ['acars-status'],
    queryFn: () => api.get('/acars/status'),
    refetchInterval: 5000,
  })

  const { data: messages, isLoading, error } = useQuery<ACARSMessage[]>({
    queryKey: ['acars-messages'],
    queryFn: () => api.get('/acars/messages'),
    refetchInterval: 5000,
  })

  const clearMutation = useMutation({
    mutationFn: () => api.delete('/acars/messages'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['acars-messages'] }),
  })

  const formatTime = (ts: string) => {
    const d = new Date(ts)
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  }

  const formatDate = (ts: string) => {
    const d = new Date(ts)
    return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
  }

  const sorted = messages
    ? [...messages].sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
    : []

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">ACARS Monitor</h2>
          <p className="text-sm text-dark-400 mt-1">
            {sorted.length} message{sorted.length !== 1 ? 's' : ''}
          </p>
        </div>

        <div className="flex items-center gap-3">
          {/* Status indicator */}
          {status && (
            <div className="flex items-center gap-2 px-3 py-1.5 bg-surface rounded-lg border border-dark-700/50">
              <span className={`inline-flex w-2 h-2 rounded-full ${status.enabled ? 'bg-green-400 animate-pulse' : 'bg-dark-600'}`} />
              <span className="text-xs text-dark-300">
                {status.enabled ? status.status : 'Disabled'}
              </span>
              {status.enabled && (
                <span className="text-xs text-dark-500">
                  {status.messageCount} total
                </span>
              )}
            </div>
          )}

          <button
            onClick={() => {
              if (confirm('Clear all ACARS messages?')) clearMutation.mutate()
            }}
            className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-300 text-sm rounded font-medium transition-colors"
          >
            Clear
          </button>
        </div>
      </div>

      <div className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
        {isLoading ? (
          <div className="p-8 text-center">
            <div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
            <p className="mt-2 text-sm text-dark-400">Loading ACARS messages...</p>
          </div>
        ) : error ? (
          <div className="p-8 text-center">
            <p className="text-sm text-red-400">Failed to load ACARS data: {(error as Error).message}</p>
          </div>
        ) : sorted.length === 0 ? (
          <div className="p-8 text-center">
            <p className="text-sm text-dark-400">
              {status?.enabled ? 'No ACARS messages received' : 'ACARS receiver not enabled'}
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Date</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Time</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Flight</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Label</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Reg</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3 min-w-[300px]">Message</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Freq</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Level</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {sorted.map((msg, idx) => (
                  <tr
                    key={msg.id || idx}
                    className="hover:bg-dark-800/30 transition-colors"
                  >
                    <td className="px-4 py-3 text-sm text-dark-400 whitespace-nowrap">{formatDate(msg.timestamp)}</td>
                    <td className="px-4 py-3 text-sm text-dark-300 font-mono whitespace-nowrap">{formatTime(msg.timestamp)}</td>
                    <td className="px-4 py-3">
                      <span className="text-sm text-dark-200 font-medium">
                        {msg.flight?.trim() || '-'}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-300 font-mono">{msg.label || '-'}</td>
                    <td className="px-4 py-3 text-sm text-dark-400 font-mono">{msg.registrationNo || '-'}</td>
                    <td className="px-4 py-3">
                      <span className="text-sm text-dark-200 whitespace-pre-wrap break-all">
                        {msg.text?.trim() || '-'}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-400 text-right font-mono">
                      {msg.frequency ? `${msg.frequency.toFixed(3)}` : '-'}
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-400 text-right font-mono">
                      {msg.level !== undefined ? msg.level : '-'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
