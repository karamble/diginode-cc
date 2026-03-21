import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
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

const COMMAND_TYPES = [
  'SCAN_START',
  'SCAN_STOP',
  'DEAUTH_START',
  'DEAUTH_STOP',
  'TRIANGULATE',
  'REBOOT',
  'SHUTDOWN',
  'DISPLAY_CONFIG',
  'BLUETOOTH_CONFIG',
] as const

type CommandType = typeof COMMAND_TYPES[number]

const RF_ENVIRONMENTS = [
  'indoor',
  'outdoor',
  'urban',
  'dense_urban',
  'tunnel',
  'dense_forest',
] as const

const inputClass = 'w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500 placeholder-dark-500'
const labelClass = 'block text-xs font-medium text-dark-400 mb-1'

interface CommandFormState {
  targetNode: string
  commandType: CommandType
  // SCAN_START
  scanChannels: string
  scanDuration: string
  // DEAUTH_START
  deauthTargetMac: string
  deauthDuration: string
  // TRIANGULATE
  triDuration: string
  triRfEnv: string
  // DISPLAY_CONFIG
  screenOnSecs: string
  // BLUETOOTH_CONFIG
  btEnabled: boolean
  btMode: string
  btFixedPin: string
  // SHUTDOWN
  shutdownSeconds: string
  // Generic fallback
  genericParams: string
}

const defaultFormState: CommandFormState = {
  targetNode: '',
  commandType: 'SCAN_START',
  scanChannels: '',
  scanDuration: '',
  deauthTargetMac: '',
  deauthDuration: '',
  triDuration: '',
  triRfEnv: 'outdoor',
  screenOnSecs: '',
  btEnabled: true,
  btMode: '',
  btFixedPin: '',
  shutdownSeconds: '',
  genericParams: '{}',
}

function buildPayload(form: CommandFormState): Record<string, unknown> {
  switch (form.commandType) {
    case 'SCAN_START': {
      const payload: Record<string, unknown> = {}
      if (form.scanChannels.trim()) {
        payload.channels = form.scanChannels.split(',').map(s => s.trim()).filter(Boolean)
      }
      if (form.scanDuration.trim()) {
        payload.duration = Number(form.scanDuration)
      }
      return payload
    }
    case 'DEAUTH_START': {
      const payload: Record<string, unknown> = {}
      if (form.deauthTargetMac.trim()) {
        payload.targetMac = form.deauthTargetMac.trim()
      }
      if (form.deauthDuration.trim()) {
        payload.duration = Number(form.deauthDuration)
      }
      return payload
    }
    case 'TRIANGULATE': {
      const payload: Record<string, unknown> = {}
      if (form.triDuration.trim()) {
        payload.duration = Number(form.triDuration)
      }
      if (form.triRfEnv) {
        payload.rfEnvironment = form.triRfEnv
      }
      return payload
    }
    case 'DISPLAY_CONFIG': {
      const payload: Record<string, unknown> = {}
      if (form.screenOnSecs.trim()) {
        payload.screenOnSecs = Number(form.screenOnSecs)
      }
      return payload
    }
    case 'BLUETOOTH_CONFIG': {
      const payload: Record<string, unknown> = {
        enabled: form.btEnabled,
      }
      if (form.btMode.trim()) {
        payload.mode = form.btMode.trim()
      }
      if (form.btFixedPin.trim()) {
        payload.fixedPin = form.btFixedPin.trim()
      }
      return payload
    }
    case 'SHUTDOWN': {
      const payload: Record<string, unknown> = {}
      if (form.shutdownSeconds.trim()) {
        payload.seconds = Number(form.shutdownSeconds)
      }
      return payload
    }
    case 'SCAN_STOP':
    case 'DEAUTH_STOP':
    case 'REBOOT':
      return {}
    default: {
      try {
        return JSON.parse(form.genericParams)
      } catch {
        return {}
      }
    }
  }
}

function hasTypeSpecificParams(cmdType: CommandType): boolean {
  return ['SCAN_START', 'DEAUTH_START', 'TRIANGULATE', 'DISPLAY_CONFIG', 'BLUETOOTH_CONFIG', 'SHUTDOWN'].includes(cmdType)
}

export default function CommandsPage() {
  const queryClient = useQueryClient()
  const [showSend, setShowSend] = useState(false)
  const [form, setForm] = useState<CommandFormState>({ ...defaultFormState })

  const { data: commands, isLoading, error } = useQuery<Command[]>({
    queryKey: ['commands'],
    queryFn: () => api.get('/commands'),
    refetchInterval: 5000,
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/commands/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['commands'] }),
  })

  const sendMutation = useMutation({
    mutationFn: (body: { targetNode: number; commandType: string; payload: Record<string, unknown> }) =>
      api.post('/commands', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['commands'] })
      setForm({ ...defaultFormState })
      setShowSend(false)
    },
  })

  const handleSend = () => {
    const targetNode = parseInt(form.targetNode, 10)
    if (isNaN(targetNode) || targetNode <= 0) return

    sendMutation.mutate({
      targetNode,
      commandType: form.commandType,
      payload: buildPayload(form),
    })
  }

  const updateField = <K extends keyof CommandFormState>(key: K, value: CommandFormState[K]) => {
    setForm(prev => ({ ...prev, [key]: value }))
  }

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

  const renderParamsSection = () => {
    switch (form.commandType) {
      case 'SCAN_START':
        return (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label className={labelClass}>Channels</label>
              <input
                type="text"
                placeholder="1, 6, 11, 36, ..."
                value={form.scanChannels}
                onChange={e => updateField('scanChannels', e.target.value)}
                className={inputClass + ' font-mono'}
              />
              <p className="text-[10px] text-dark-600 mt-0.5">Comma-separated channel numbers</p>
            </div>
            <div>
              <label className={labelClass}>Duration (seconds)</label>
              <input
                type="number"
                min={0}
                placeholder="60"
                value={form.scanDuration}
                onChange={e => updateField('scanDuration', e.target.value)}
                className={inputClass + ' font-mono'}
              />
            </div>
          </div>
        )
      case 'DEAUTH_START':
        return (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label className={labelClass}>Target MAC</label>
              <input
                type="text"
                placeholder="AA:BB:CC:DD:EE:FF"
                value={form.deauthTargetMac}
                onChange={e => updateField('deauthTargetMac', e.target.value)}
                className={inputClass + ' font-mono'}
              />
            </div>
            <div>
              <label className={labelClass}>Duration (seconds)</label>
              <input
                type="number"
                min={0}
                placeholder="30"
                value={form.deauthDuration}
                onChange={e => updateField('deauthDuration', e.target.value)}
                className={inputClass + ' font-mono'}
              />
            </div>
          </div>
        )
      case 'TRIANGULATE':
        return (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label className={labelClass}>Duration (seconds)</label>
              <input
                type="number"
                min={0}
                placeholder="120"
                value={form.triDuration}
                onChange={e => updateField('triDuration', e.target.value)}
                className={inputClass + ' font-mono'}
              />
            </div>
            <div>
              <label className={labelClass}>RF Environment</label>
              <select
                value={form.triRfEnv}
                onChange={e => updateField('triRfEnv', e.target.value)}
                className={inputClass}
              >
                {RF_ENVIRONMENTS.map(env => (
                  <option key={env} value={env}>{env.replace(/_/g, ' ')}</option>
                ))}
              </select>
            </div>
          </div>
        )
      case 'DISPLAY_CONFIG':
        return (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label className={labelClass}>Screen On Seconds</label>
              <input
                type="number"
                min={0}
                placeholder="60"
                value={form.screenOnSecs}
                onChange={e => updateField('screenOnSecs', e.target.value)}
                className={inputClass + ' font-mono'}
              />
              <p className="text-[10px] text-dark-600 mt-0.5">0 = always on</p>
            </div>
          </div>
        )
      case 'BLUETOOTH_CONFIG':
        return (
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <div>
              <label className={labelClass}>Enabled</label>
              <button
                onClick={() => updateField('btEnabled', !form.btEnabled)}
                className={`relative inline-flex h-9 w-16 items-center rounded transition-colors ${
                  form.btEnabled ? 'bg-primary-600' : 'bg-dark-700'
                }`}
              >
                <span
                  className={`inline-block w-6 h-6 rounded bg-white shadow transform transition-transform ${
                    form.btEnabled ? 'translate-x-8' : 'translate-x-1.5'
                  }`}
                />
              </button>
            </div>
            <div>
              <label className={labelClass}>Mode</label>
              <input
                type="text"
                placeholder="e.g. RANDOM_PIN"
                value={form.btMode}
                onChange={e => updateField('btMode', e.target.value)}
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass}>Fixed PIN</label>
              <input
                type="text"
                placeholder="e.g. 123456"
                value={form.btFixedPin}
                onChange={e => updateField('btFixedPin', e.target.value)}
                className={inputClass + ' font-mono'}
              />
            </div>
          </div>
        )
      case 'SHUTDOWN':
        return (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label className={labelClass}>Delay (seconds)</label>
              <input
                type="number"
                min={0}
                placeholder="0"
                value={form.shutdownSeconds}
                onChange={e => updateField('shutdownSeconds', e.target.value)}
                className={inputClass + ' font-mono'}
              />
              <p className="text-[10px] text-dark-600 mt-0.5">0 = immediate</p>
            </div>
          </div>
        )
      case 'SCAN_STOP':
      case 'DEAUTH_STOP':
      case 'REBOOT':
        return (
          <p className="text-xs text-dark-500 italic">No parameters required for {form.commandType}</p>
        )
      default:
        return null
    }
  }

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">Commands</h2>
          <p className="text-sm text-dark-400 mt-1">
            {commands?.filter(c => c.status === 'PENDING' || c.status === 'SENT').length || 0} pending / {commands?.length || 0} total
          </p>
        </div>
        <button
          onClick={() => setShowSend(!showSend)}
          className="px-4 py-2 bg-primary-600 hover:bg-primary-700 text-white text-sm rounded font-medium transition-colors"
        >
          {showSend ? 'Cancel' : 'Send Command'}
        </button>
      </div>

      {/* Send Command Form */}
      {showSend && (
        <div className="bg-surface rounded-xl border border-dark-700/50 overflow-hidden">
          <div className="px-4 py-3 border-b border-dark-700/50">
            <h3 className="text-sm font-medium text-dark-200 flex items-center gap-2">
              <svg className="w-4 h-4 text-primary-400" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 12L3.269 3.126A59.768 59.768 0 0121.485 12 59.77 59.77 0 013.27 20.876L5.999 12zm0 0h7.5" />
              </svg>
              Send Command
            </h3>
          </div>
          <div className="p-4 space-y-4">
            {/* Row 1: Target Node + Command Type */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label className={labelClass}>Target Node *</label>
                <input
                  type="number"
                  min={1}
                  placeholder="Mesh node number (decimal)"
                  value={form.targetNode}
                  onChange={e => updateField('targetNode', e.target.value)}
                  className={inputClass + ' font-mono'}
                />
                {form.targetNode && !isNaN(parseInt(form.targetNode, 10)) && parseInt(form.targetNode, 10) > 0 && (
                  <p className="text-[10px] text-dark-500 mt-0.5 font-mono">
                    Hex: !{parseInt(form.targetNode, 10).toString(16).padStart(8, '0')}
                  </p>
                )}
              </div>
              <div>
                <label className={labelClass}>Command Type *</label>
                <select
                  value={form.commandType}
                  onChange={e => updateField('commandType', e.target.value as CommandType)}
                  className={inputClass}
                >
                  {COMMAND_TYPES.map(ct => (
                    <option key={ct} value={ct}>{ct}</option>
                  ))}
                </select>
              </div>
            </div>

            {/* Dynamic Parameters */}
            <div>
              <div className="text-xs font-medium text-dark-300 mb-2 uppercase tracking-wider">Parameters</div>
              {renderParamsSection()}

              {/* Generic JSON fallback for types not explicitly handled */}
              {!hasTypeSpecificParams(form.commandType) && form.commandType !== 'SCAN_STOP' && form.commandType !== 'DEAUTH_STOP' && form.commandType !== 'REBOOT' && (
                <div>
                  <label className={labelClass}>Parameters (JSON)</label>
                  <textarea
                    rows={3}
                    placeholder='{"key": "value"}'
                    value={form.genericParams}
                    onChange={e => updateField('genericParams', e.target.value)}
                    className={inputClass + ' font-mono resize-none'}
                  />
                  <p className="text-[10px] text-dark-600 mt-0.5">Raw JSON payload</p>
                </div>
              )}
            </div>

            {/* Actions */}
            <div className="flex items-center justify-between pt-2 border-t border-dark-700/50">
              <p className="text-[10px] text-dark-500">
                Commands are rate-limited to 1 per node per 2 seconds. Max 3 retries.
              </p>
              <div className="flex gap-2">
                <button
                  onClick={() => { setShowSend(false); setForm({ ...defaultFormState }) }}
                  className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-300 text-sm rounded font-medium transition-colors"
                >
                  Cancel
                </button>
                <button
                  onClick={handleSend}
                  disabled={!form.targetNode || isNaN(parseInt(form.targetNode, 10)) || parseInt(form.targetNode, 10) <= 0 || sendMutation.isPending}
                  className="px-4 py-2 bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors"
                >
                  {sendMutation.isPending ? 'Sending...' : 'Send'}
                </button>
              </div>
            </div>
            {sendMutation.isError && (
              <p className="text-sm text-red-400">{(sendMutation.error as Error).message}</p>
            )}
          </div>
        </div>
      )}

      {/* Command History Table */}
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
