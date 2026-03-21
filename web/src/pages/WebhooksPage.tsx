import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import api from '../api/client'

interface Webhook {
  id: string
  name: string
  url: string
  method: string
  headers?: Record<string, string>
  secret?: string
  events: string[]
  enabled: boolean
  lastTriggered?: string
  lastStatus?: number
}

export default function WebhooksPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [newWebhook, setNewWebhook] = useState({
    name: '',
    url: '',
    method: 'POST',
    events: '*',
    enabled: true,
  })
  const [testingId, setTestingId] = useState<string | null>(null)

  const { data: webhooks, isLoading, error } = useQuery<Webhook[]>({
    queryKey: ['webhooks'],
    queryFn: () => api.get('/webhooks'),
  })

  const createMutation = useMutation({
    mutationFn: (body: { name: string; url: string; method: string; events: string[]; enabled: boolean }) =>
      api.post('/webhooks', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['webhooks'] })
      setShowCreate(false)
      setNewWebhook({ name: '', url: '', method: 'POST', events: '*', enabled: true })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/webhooks/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['webhooks'] }),
  })

  const testMutation = useMutation({
    mutationFn: (id: string) => {
      setTestingId(id)
      return api.post(`/webhooks/${id}/test`)
    },
    onSettled: () => {
      setTestingId(null)
      queryClient.invalidateQueries({ queryKey: ['webhooks'] })
    },
  })

  const formatDate = (dateStr?: string) => {
    if (!dateStr) return 'Never'
    const d = new Date(dateStr)
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }

  const statusCodeColor = (code?: number) => {
    if (!code) return 'text-dark-500'
    if (code >= 200 && code < 300) return 'text-green-400'
    if (code >= 400 && code < 500) return 'text-yellow-400'
    return 'text-red-400'
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-lg font-semibold text-dark-100">Webhooks</h2>
        <button
          onClick={() => setShowCreate(!showCreate)}
          className="px-4 py-2 bg-primary-600 hover:bg-primary-700 text-white text-sm rounded font-medium transition-colors"
        >
          {showCreate ? 'Cancel' : 'Add Webhook'}
        </button>
      </div>

      {showCreate && (
        <div className="bg-surface rounded-lg border border-dark-700/50 p-4 mb-6">
          <h3 className="text-sm font-medium text-dark-200 mb-3">Create Webhook</h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
            <input
              type="text"
              placeholder="Name"
              value={newWebhook.name}
              onChange={(e) => setNewWebhook({ ...newWebhook, name: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <input
              type="url"
              placeholder="https://example.com/webhook"
              value={newWebhook.url}
              onChange={(e) => setNewWebhook({ ...newWebhook, url: e.target.value })}
              className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
            />
            <div className="flex gap-2">
              <select
                value={newWebhook.method}
                onChange={(e) => setNewWebhook({ ...newWebhook, method: e.target.value })}
                className="px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
              >
                <option value="POST">POST</option>
                <option value="PUT">PUT</option>
                <option value="PATCH">PATCH</option>
              </select>
              <input
                type="text"
                placeholder="Events (comma-separated or *)"
                value={newWebhook.events}
                onChange={(e) => setNewWebhook({ ...newWebhook, events: e.target.value })}
                className="flex-1 px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500"
              />
            </div>
          </div>
          <div className="mt-3 flex justify-end">
            <button
              onClick={() => createMutation.mutate({
                ...newWebhook,
                events: newWebhook.events.split(',').map(e => e.trim()).filter(Boolean),
              })}
              disabled={!newWebhook.name || !newWebhook.url || createMutation.isPending}
              className="px-4 py-2 bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors"
            >
              {createMutation.isPending ? 'Creating...' : 'Create'}
            </button>
          </div>
          {createMutation.isError && (
            <p className="mt-2 text-sm text-red-400">{(createMutation.error as Error).message}</p>
          )}
        </div>
      )}

      <div className="bg-surface rounded-lg border border-dark-700/50 overflow-hidden">
        {isLoading ? (
          <div className="p-8 text-center">
            <div className="inline-block w-6 h-6 border-2 border-primary-500 border-t-transparent rounded-full animate-spin" />
            <p className="mt-2 text-sm text-dark-400">Loading webhooks...</p>
          </div>
        ) : error ? (
          <div className="p-8 text-center">
            <p className="text-sm text-red-400">Failed to load webhooks: {(error as Error).message}</p>
          </div>
        ) : !webhooks || webhooks.length === 0 ? (
          <div className="p-8 text-center">
            <p className="text-sm text-dark-400">No webhooks configured</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-dark-700/50">
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Name</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">URL</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Method</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Events</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Enabled</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Last Status</th>
                  <th className="text-left text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Last Triggered</th>
                  <th className="text-right text-xs font-medium text-dark-400 uppercase tracking-wider px-4 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-dark-700/30">
                {webhooks.map((wh) => (
                  <tr key={wh.id} className="hover:bg-dark-800/30 transition-colors">
                    <td className="px-4 py-3 text-sm text-dark-200 font-medium">{wh.name}</td>
                    <td className="px-4 py-3">
                      <span className="text-sm text-dark-300 font-mono truncate block max-w-[240px]" title={wh.url}>
                        {wh.url}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <span className="inline-flex px-2 py-0.5 text-xs font-medium rounded bg-dark-700/50 text-dark-300 border border-dark-600">
                        {wh.method}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex flex-wrap gap-1">
                        {wh.events?.map((ev, i) => (
                          <span key={i} className="inline-flex px-1.5 py-0.5 text-xs rounded bg-primary-600/20 text-primary-400 border border-primary-500/30">
                            {ev}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      {wh.enabled ? (
                        <span className="inline-flex w-2 h-2 rounded-full bg-green-400" />
                      ) : (
                        <span className="inline-flex w-2 h-2 rounded-full bg-dark-600" />
                      )}
                    </td>
                    <td className="px-4 py-3">
                      {wh.lastStatus ? (
                        <span className={`text-sm font-mono ${statusCodeColor(wh.lastStatus)}`}>
                          {wh.lastStatus}
                        </span>
                      ) : (
                        <span className="text-xs text-dark-500">-</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs text-dark-400">{formatDate(wh.lastTriggered)}</td>
                    <td className="px-4 py-3 text-right space-x-3">
                      <button
                        onClick={() => testMutation.mutate(wh.id)}
                        disabled={testingId === wh.id}
                        className="text-xs text-primary-400 hover:text-primary-300 disabled:opacity-50 transition-colors"
                      >
                        {testingId === wh.id ? 'Testing...' : 'Test'}
                      </button>
                      <button
                        onClick={() => {
                          if (confirm(`Delete webhook "${wh.name}"?`)) deleteMutation.mutate(wh.id)
                        }}
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
