import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, useMemo } from 'react'
import api from '../api/client'

interface ParamDef {
  key: string
  label: string
  type: string
  required?: boolean
  min?: number
  max?: number
  options?: string[]
  placeholder?: string
}

interface CommandDef {
  name: string
  group: string
  description: string
  params: ParamDef[]
  allowForever?: boolean
  singleNode?: boolean
}

interface CommandRecord {
  id: string
  target?: string
  name?: string
  params?: string[]
  line?: string
  commandType: string
  status: string
  sentAt?: string
  ackedAt?: string
  finishedAt?: string
  createdAt: string
  ackKind?: string
  ackStatus?: string
  ackNode?: string
  resultText?: string
  errorText?: string
}

interface NodeRow {
  id: string
  nodeNum: number
  name: string
  shortName?: string
  isOnline: boolean
  nodeType?: string
}

function statusBadge(status: string) {
  switch (status) {
    case 'OK': case 'ACKED': return 'bg-green-500/20 text-green-400 border-green-500/40'
    case 'SENT': return 'bg-blue-500/20 text-blue-400 border-blue-500/40'
    case 'PENDING': return 'bg-yellow-500/20 text-yellow-400 border-yellow-500/40'
    case 'ERROR': case 'FAILED': return 'bg-red-500/20 text-red-400 border-red-500/40'
    case 'TIMEOUT': return 'bg-dark-600/20 text-dark-400 border-dark-600/40'
    default: return 'bg-dark-700/20 text-dark-400 border-dark-600/40'
  }
}

function formatAge(dateStr?: string): string {
  if (!dateStr) return '-'
  const diff = Date.now() - new Date(dateStr).getTime()
  const sec = Math.floor(diff / 1000)
  if (sec < 60) return `${sec}s ago`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  return `${Math.floor(min / 60)}h ago`
}

const GROUP_ORDER = ['Status', 'Scanning', 'Detection', 'Triangulation', 'Configuration', 'Security', 'Battery', 'System']

export default function CommandsPage() {
  const queryClient = useQueryClient()
  const [selectedCmd, setSelectedCmd] = useState<string>('')
  const [target, setTarget] = useState('@ALL')
  const [paramValues, setParamValues] = useState<Record<string, string>>({})
  const [forever, setForever] = useState(false)

  const { data: cmdTypes = [] } = useQuery<CommandDef[]>({
    queryKey: ['command-types'],
    queryFn: () => api.get('/commands/types'),
  })

  const { data: commands = [] } = useQuery<CommandRecord[]>({
    queryKey: ['commands'],
    queryFn: () => api.get('/commands'),
    refetchInterval: 5000,
  })

  const { data: nodes = [] } = useQuery<NodeRow[]>({
    queryKey: ['nodes'],
    queryFn: () => api.get('/nodes'),
    refetchInterval: 10000,
  })

  const sendMutation = useMutation({
    mutationFn: (body: { target: string; name: string; params: string[]; forever: boolean }) =>
      api.post('/commands', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['commands'] })
      setParamValues({})
      setForever(false)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.delete(`/commands/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['commands'] }),
  })

  // Group commands by category
  const grouped = useMemo(() => {
    const groups: Record<string, CommandDef[]> = {}
    cmdTypes.forEach(c => {
      if (!groups[c.group]) groups[c.group] = []
      groups[c.group].push(c)
    })
    return groups
  }, [cmdTypes])

  const activeDef = cmdTypes.find(c => c.name === selectedCmd)

  const handleSend = () => {
    if (!selectedCmd) return
    const params = (activeDef?.params || []).map(p => paramValues[p.key] || '')
    sendMutation.mutate({ target, name: selectedCmd, params, forever })
  }

  const setParam = (key: string, val: string) => {
    setParamValues(prev => ({ ...prev, [key]: val }))
  }

  const onlineNodes = nodes.filter(n => n.isOnline)

  return (
    <div className="p-6 space-y-6">
      {/* Command Builder */}
      <div className="bg-surface rounded-xl border border-dark-700/50 p-5">
        <h2 className="text-sm font-semibold text-dark-100 mb-4">Command Console</h2>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-4">
          {/* Target selector */}
          <div>
            <label className="text-[11px] text-dark-500 block mb-1">Target</label>
            <select
              value={target}
              onChange={e => setTarget(e.target.value)}
              className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-sm text-dark-200 focus:border-primary-500 focus:outline-none"
            >
              <option value="@ALL">@ALL (broadcast)</option>
              {onlineNodes.map(n => (
                <option key={n.id} value={`@NODE_${n.shortName || n.nodeNum}`}>
                  @NODE_{n.shortName || n.nodeNum} — {n.name}
                  {n.nodeType === 'antihunter' ? ' [AH]' : ' [GTM]'}
                </option>
              ))}
            </select>
          </div>

          {/* Command selector */}
          <div>
            <label className="text-[11px] text-dark-500 block mb-1">Command</label>
            <select
              value={selectedCmd}
              onChange={e => { setSelectedCmd(e.target.value); setParamValues({}); setForever(false) }}
              className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-sm text-dark-200 focus:border-primary-500 focus:outline-none"
            >
              <option value="">Select command...</option>
              {GROUP_ORDER.filter(g => grouped[g]).map(group => (
                <optgroup key={group} label={group}>
                  {grouped[group].map(cmd => (
                    <option key={cmd.name} value={cmd.name}>{cmd.name}</option>
                  ))}
                </optgroup>
              ))}
            </select>
          </div>

          {/* Send button */}
          <div className="flex items-end">
            <button
              onClick={handleSend}
              disabled={!selectedCmd || sendMutation.isPending}
              className="w-full px-4 py-2 bg-primary-600 hover:bg-primary-500 disabled:bg-dark-700 disabled:text-dark-500 text-white text-sm rounded font-medium transition-colors"
            >
              {sendMutation.isPending ? 'Sending...' : 'Send Command'}
            </button>
          </div>
        </div>

        {/* Dynamic parameter form */}
        {activeDef && activeDef.params.length > 0 && (
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
            {activeDef.params.map(p => (
              <div key={p.key}>
                <label className="text-[10px] text-dark-500 block mb-1">
                  {p.label} {p.required && <span className="text-red-400">*</span>}
                </label>
                {p.type === 'select' ? (
                  <select
                    value={paramValues[p.key] || ''}
                    onChange={e => setParam(p.key, e.target.value)}
                    className="w-full px-2 py-1.5 bg-dark-800 border border-dark-600 rounded text-xs text-dark-200 focus:border-primary-500 focus:outline-none"
                  >
                    <option value="">--</option>
                    {p.options?.map(opt => (
                      <option key={opt} value={opt}>
                        {p.key === 'mode' ? `${opt} (${opt === '0' ? 'WiFi' : opt === '1' ? 'BLE' : 'Both'})` :
                         p.key === 'rfEnv' ? `${opt} (${['Open Sky','Suburban','Indoor','Dense','Industrial'][Number(opt)] || opt})` :
                         opt}
                      </option>
                    ))}
                  </select>
                ) : (
                  <input
                    type={p.type === 'number' || p.type === 'duration' ? 'number' : 'text'}
                    value={paramValues[p.key] || ''}
                    onChange={e => setParam(p.key, e.target.value)}
                    placeholder={p.placeholder || ''}
                    min={p.min}
                    max={p.max}
                    className="w-full px-2 py-1.5 bg-dark-800 border border-dark-600 rounded text-xs text-dark-200 font-mono focus:border-primary-500 focus:outline-none"
                  />
                )}
              </div>
            ))}
          </div>
        )}

        {/* Forever toggle */}
        {activeDef?.allowForever && (
          <label className="flex items-center gap-2 text-xs text-dark-400 mb-3 cursor-pointer">
            <input
              type="checkbox"
              checked={forever}
              onChange={e => setForever(e.target.checked)}
              className="rounded border-dark-600 bg-dark-800"
            />
            Run continuously (FOREVER)
          </label>
        )}

        {/* Command description */}
        {activeDef && (
          <p className="text-[10px] text-dark-500">{activeDef.description}</p>
        )}

        {/* Error display */}
        {sendMutation.isError && (
          <p className="text-xs text-red-400 mt-2">
            {(sendMutation.error as Error).message}
          </p>
        )}
      </div>

      {/* Command History */}
      <div className="bg-surface rounded-xl border border-dark-700/50 overflow-hidden">
        <div className="px-5 py-3 border-b border-dark-700/50">
          <h2 className="text-sm font-semibold text-dark-100">
            Command History
            <span className="ml-2 text-dark-400 font-normal">({commands.length})</span>
          </h2>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-dark-700/50 text-dark-400 uppercase tracking-wider">
                <th className="text-left px-4 py-2.5">Target</th>
                <th className="text-left px-4 py-2.5">Command</th>
                <th className="text-left px-4 py-2.5">Line</th>
                <th className="text-left px-4 py-2.5">Status</th>
                <th className="text-left px-4 py-2.5">ACK</th>
                <th className="text-left px-4 py-2.5">Sent</th>
                <th className="text-right px-4 py-2.5">Actions</th>
              </tr>
            </thead>
            <tbody>
              {commands.length === 0 ? (
                <tr><td colSpan={7} className="px-4 py-8 text-center text-dark-500">No commands sent yet</td></tr>
              ) : commands.map((cmd) => (
                <tr key={cmd.id} className="border-b border-dark-700/30 hover:bg-dark-800/30">
                  <td className="px-4 py-2 text-dark-300 font-mono">{cmd.target || '-'}</td>
                  <td className="px-4 py-2 text-dark-200 font-medium">{cmd.name || cmd.commandType}</td>
                  <td className="px-4 py-2 text-dark-400 font-mono text-[10px] max-w-[200px] truncate" title={cmd.line}>
                    {cmd.line || '-'}
                  </td>
                  <td className="px-4 py-2">
                    <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium border ${statusBadge(cmd.status)}`}>
                      {cmd.status}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-dark-400">
                    {cmd.ackKind ? (
                      <span className="text-[10px]">
                        {cmd.ackKind}
                        {cmd.ackNode && <span className="text-dark-500 ml-1">({cmd.ackNode})</span>}
                      </span>
                    ) : '-'}
                  </td>
                  <td className="px-4 py-2 text-dark-500">{formatAge(cmd.sentAt || cmd.createdAt)}</td>
                  <td className="px-4 py-2 text-right">
                    <button
                      onClick={() => deleteMutation.mutate(cmd.id)}
                      className="text-dark-600 hover:text-red-400 transition-colors"
                      title="Delete"
                    >
                      <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
