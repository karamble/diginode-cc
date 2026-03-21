import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from '../api/client'

interface Command {
  id: string
  targetNode: number
  commandType: string
  payload?: Record<string, unknown>
  status: 'PENDING' | 'SENT' | 'ACKED' | 'OK' | 'FAILED' | 'ERROR' | 'TIMEOUT'
  sentAt?: string
  ackedAt?: string
  result?: Record<string, unknown>
  retryCount: number
  maxRetries: number
  createdAt: string
}

const statusBadge: Record<string, string> = {
  PENDING: 'bg-yellow-600/20 text-yellow-400 border-yellow-500/30',
  SENT: 'bg-blue-600/20 text-blue-400 border-blue-500/30',
  ACKED: 'bg-green-600/20 text-green-400 border-green-500/30',
  OK: 'bg-green-600/20 text-green-400 border-green-500/30',
  FAILED: 'bg-red-600/20 text-red-400 border-red-500/30',
  ERROR: 'bg-red-600/20 text-red-400 border-red-500/30',
  TIMEOUT: 'bg-dark-600/20 text-dark-400 border-dark-500/30',
}

export default function CommandsPage() {
  const queryClient = useQueryClient()

  const { data: commands, isLoading, error } = useQuery<Command[]>({
    queryKey: ['commands'],
    queryFn: () => api.get('/commands'),
    refetchInterval: 5000,
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/commands/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['commands'] }),
  })

  const formatDate = (dateStr?: string) => {
    if (!dateStr) return '-'
    const d = new Date(dateStr)
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) +
      ' ' + d.toLocaleDateString()
  }

  const formatNodeId = (num: number) => {
    if (!num) return '-'
    return '!' + num.toString(16).padStart(8, '0')
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">Commands</h2>
          <p className="text-sm text-dark-400 mt-1">
            {commands?.filter(c => c.status === 'PENDING' || c.status === 'SENT').length || 0} pending / {commands?.length || 0} total
          </p>
        </div>
      </div>

      <div className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
        {isLoading ? (
          <div className="p-8 text-center">
            <div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
            <p className="mt-2 text-sm text-dark-400">Loading commands...</p>
          </div>
        ) : error ? (
          <div className="p-8 text-center">
            <p className="text-sm text-red-400">Failed to load commands: {(error as Error).message}</p>
          </div>
        ) : !commands || commands.length === 0 ? (
          <div className="p-8 text-center">
            <p className="text-sm text-dark-400">No commands in queue</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">ID</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Target Node</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Type</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Status</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Sent</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Acked</th>
                  <th className="text-center text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Retries</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {commands.map((cmd) => (
                  <tr key={cmd.id} className="hover:bg-dark-800/30 transition-colors">
                    <td className="px-4 py-3">
                      <span className="text-xs text-dark-300 font-mono">{cmd.id.substring(0, 8)}...</span>
                    </td>
                    <td className="px-4 py-3 text-sm text-dark-200 font-mono">{formatNodeId(cmd.targetNode)}</td>
                    <td className="px-4 py-3 text-sm text-dark-300">{cmd.commandType}</td>
                    <td className="px-4 py-3">
                      <span className={`inline-flex px-2 py-0.5 text-xs font-medium rounded border ${statusBadge[cmd.status] || statusBadge.PENDING}`}>
                        {cmd.status}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-xs text-dark-400">{formatDate(cmd.sentAt)}</td>
                    <td className="px-4 py-3 text-xs text-dark-400">{formatDate(cmd.ackedAt)}</td>
                    <td className="px-4 py-3 text-center">
                      <span className="text-xs text-dark-400 font-mono">{cmd.retryCount}/{cmd.maxRetries}</span>
                    </td>
                    <td className="px-4 py-3 text-right">
                      <button
                        onClick={() => deleteMutation.mutate(cmd.id)}
                        className="text-xs text-red-400 hover:text-red-300 transition-colors"
                      >
                        Delete
                      </button>
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
