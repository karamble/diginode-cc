import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import api from '../api/client'

interface AlertRule {
  id: string
  name: string
  description?: string
  condition: Record<string, unknown>
  severity: 'INFO' | 'NOTICE' | 'ALERT' | 'CRITICAL'
  enabled: boolean
  cooldownSeconds: number
  lastTriggered?: string
}

interface AlertEvent {
  id: string
  ruleId?: string
  severity: 'INFO' | 'NOTICE' | 'ALERT' | 'CRITICAL'
  title: string
  message?: string
  data?: Record<string, unknown>
  acknowledged: boolean
  acknowledgedBy?: string
  acknowledgedAt?: string
  createdAt: string
}

type Severity = 'INFO' | 'NOTICE' | 'ALERT' | 'CRITICAL'
type MatchMode = 'ANY' | 'ALL'

interface RuleFormState {
  name: string
  description: string
  severity: Severity
  matchMode: MatchMode
  macAddresses: string
  ouiPrefixes: string
  ssids: string
  channels: string
  minRssi: string
  maxRssi: string
  messageTemplate: string
  cooldownSeconds: number
  enabled: boolean
}

const defaultForm: RuleFormState = {
  name: '',
  description: '',
  severity: 'ALERT',
  matchMode: 'ANY',
  macAddresses: '',
  ouiPrefixes: '',
  ssids: '',
  channels: '',
  minRssi: '',
  maxRssi: '',
  messageTemplate: '',
  cooldownSeconds: 300,
  enabled: true,
}

function severityBadge(s: string) {
  switch (s) {
    case 'CRITICAL': return 'badge-critical'
    case 'ALERT': return 'badge-alert'
    case 'NOTICE': return 'badge-notice'
    default: return 'badge-info'
  }
}

function severityIcon(s: string) {
  switch (s) {
    case 'CRITICAL':
      return 'M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z'
    case 'ALERT':
      return 'M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z'
    default:
      return 'M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z'
  }
}

function formatCooldown(secs: number): string {
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m`
  return `${Math.floor(secs / 3600)}h`
}

const inputClass = 'w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-100 text-sm focus:outline-none focus:border-primary-500 placeholder-dark-500'
const labelClass = 'block text-xs font-medium text-dark-400 mb-1'

function splitTrimFilter(val: string): string[] {
  return val.split(',').map(s => s.trim()).filter(Boolean)
}

function buildCondition(form: RuleFormState): Record<string, unknown> {
  const cond: Record<string, unknown> = {
    matchMode: form.matchMode,
  }

  const macs = splitTrimFilter(form.macAddresses)
  if (macs.length > 0) cond.macAddresses = macs

  const ouis = splitTrimFilter(form.ouiPrefixes)
  if (ouis.length > 0) cond.ouiPrefixes = ouis

  const ssids = splitTrimFilter(form.ssids)
  if (ssids.length > 0) cond.ssids = ssids

  const channels = splitTrimFilter(form.channels).map(Number).filter(n => !isNaN(n))
  if (channels.length > 0) cond.channels = channels

  if (form.minRssi !== '') {
    const v = Number(form.minRssi)
    if (!isNaN(v)) cond.minRssi = v
  }
  if (form.maxRssi !== '') {
    const v = Number(form.maxRssi)
    if (!isNaN(v)) cond.maxRssi = v
  }

  if (form.messageTemplate.trim()) {
    cond.messageTemplate = form.messageTemplate.trim()
  }

  return cond
}

export default function AlertsPage() {
  const queryClient = useQueryClient()
  const [eventsLimit, setEventsLimit] = useState(50)
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState<RuleFormState>({ ...defaultForm })

  // Fetch alert rules
  const { data: rules = [], isLoading: rulesLoading } = useQuery({
    queryKey: ['alertRules'],
    queryFn: () => api.get<AlertRule[]>('/alerts/rules'),
    refetchInterval: 10000,
  })

  // Fetch alert events
  const { data: events = [], isLoading: eventsLoading } = useQuery({
    queryKey: ['alertEvents', eventsLimit],
    queryFn: () => api.get<AlertEvent[]>(`/alerts/events?limit=${eventsLimit}`),
    refetchInterval: 5000,
  })

  // Toggle rule enabled
  const toggleRule = useMutation({
    mutationFn: (rule: AlertRule) =>
      api.put(`/alerts/rules/${rule.id}`, { ...rule, enabled: !rule.enabled }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['alertRules'] }),
  })

  // Delete rule
  const deleteRule = useMutation({
    mutationFn: (id: string) => api.delete(`/alerts/rules/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['alertRules'] }),
  })

  // Acknowledge event
  const ackEvent = useMutation({
    mutationFn: (id: string) => api.post(`/alerts/events/${id}/acknowledge`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['alertEvents'] }),
  })

  // Create rule
  const createRule = useMutation({
    mutationFn: (body: { name: string; description: string; condition: Record<string, unknown>; severity: Severity; enabled: boolean; cooldownSeconds: number }) =>
      api.post('/alerts/rules', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['alertRules'] })
      setShowCreate(false)
      setForm({ ...defaultForm })
    },
  })

  const handleCreate = () => {
    createRule.mutate({
      name: form.name,
      description: form.description,
      condition: buildCondition(form),
      severity: form.severity,
      enabled: form.enabled,
      cooldownSeconds: form.cooldownSeconds,
    })
  }

  const updateField = <K extends keyof RuleFormState>(key: K, value: RuleFormState[K]) => {
    setForm(prev => ({ ...prev, [key]: value }))
  }

  const unackedCount = events.filter(e => !e.acknowledged).length

  return (
    <div className="p-6 space-y-6">
      {/* Page header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-dark-100">Alerts</h2>
          <p className="text-xs text-dark-400 mt-1">
            {rules.length} rule{rules.length !== 1 ? 's' : ''} configured
            {unackedCount > 0 && (
              <span className="ml-2 text-alert-alert">{unackedCount} unacknowledged event{unackedCount !== 1 ? 's' : ''}</span>
            )}
          </p>
        </div>
        <button
          onClick={() => setShowCreate(!showCreate)}
          className="px-4 py-2 bg-primary-600 hover:bg-primary-700 text-white text-sm rounded font-medium transition-colors"
        >
          {showCreate ? 'Cancel' : 'New Rule'}
        </button>
      </div>

      {/* Create Rule Form */}
      {showCreate && (
        <div className="bg-surface rounded-xl border border-dark-700/50 overflow-hidden">
          <div className="px-4 py-3 border-b border-dark-700/50">
            <h3 className="text-sm font-medium text-dark-200 flex items-center gap-2">
              <svg className="w-4 h-4 text-primary-400" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
              </svg>
              Create Alert Rule
            </h3>
          </div>
          <div className="p-4 space-y-4">
            {/* Row 1: Name, Description */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label className={labelClass}>Name *</label>
                <input
                  type="text"
                  placeholder="e.g. Known Device Alert"
                  value={form.name}
                  onChange={e => updateField('name', e.target.value)}
                  className={inputClass}
                />
              </div>
              <div>
                <label className={labelClass}>Description</label>
                <textarea
                  placeholder="Optional description of this rule"
                  value={form.description}
                  onChange={e => updateField('description', e.target.value)}
                  rows={1}
                  className={inputClass + ' resize-none'}
                />
              </div>
            </div>

            {/* Row 2: Severity, Match Mode, Cooldown, Enabled */}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
              <div>
                <label className={labelClass}>Severity</label>
                <div className="flex gap-1">
                  {(['INFO', 'NOTICE', 'ALERT', 'CRITICAL'] as Severity[]).map(s => (
                    <button
                      key={s}
                      onClick={() => updateField('severity', s)}
                      className={`flex-1 px-1.5 py-1.5 rounded text-[10px] font-medium border transition-colors ${
                        form.severity === s
                          ? severityBadge(s) + ' border-current'
                          : 'bg-dark-800 text-dark-500 border-dark-700 hover:border-dark-500'
                      }`}
                    >
                      {s}
                    </button>
                  ))}
                </div>
              </div>
              <div>
                <label className={labelClass}>Match Mode</label>
                <div className="flex rounded overflow-hidden border border-dark-600">
                  {(['ANY', 'ALL'] as MatchMode[]).map(m => (
                    <button
                      key={m}
                      onClick={() => updateField('matchMode', m)}
                      className={`flex-1 px-3 py-2 text-xs font-medium transition-colors ${
                        form.matchMode === m
                          ? 'bg-primary-600 text-white'
                          : 'bg-dark-800 text-dark-400 hover:text-dark-200'
                      }`}
                    >
                      {m}
                    </button>
                  ))}
                </div>
              </div>
              <div>
                <label className={labelClass}>Cooldown (seconds)</label>
                <input
                  type="number"
                  min={0}
                  value={form.cooldownSeconds}
                  onChange={e => updateField('cooldownSeconds', Math.max(0, Number(e.target.value)))}
                  className={inputClass}
                />
              </div>
              <div>
                <label className={labelClass}>Enabled</label>
                <button
                  onClick={() => updateField('enabled', !form.enabled)}
                  className={`relative inline-flex h-9 w-16 items-center rounded transition-colors ${
                    form.enabled ? 'bg-primary-600' : 'bg-dark-700'
                  }`}
                >
                  <span
                    className={`inline-block w-6 h-6 rounded bg-white shadow transform transition-transform ${
                      form.enabled ? 'translate-x-8' : 'translate-x-1.5'
                    }`}
                  />
                </button>
              </div>
            </div>

            {/* Conditions Section */}
            <div>
              <div className="text-xs font-medium text-dark-300 mb-2 uppercase tracking-wider">Conditions</div>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                <div>
                  <label className={labelClass}>MAC Addresses</label>
                  <input
                    type="text"
                    placeholder="AA:BB:CC:DD:EE:FF, ..."
                    value={form.macAddresses}
                    onChange={e => updateField('macAddresses', e.target.value)}
                    className={inputClass + ' font-mono'}
                  />
                  <p className="text-[10px] text-dark-600 mt-0.5">Comma-separated</p>
                </div>
                <div>
                  <label className={labelClass}>OUI Prefixes</label>
                  <input
                    type="text"
                    placeholder="DC:A6:32, B8:27:EB, ..."
                    value={form.ouiPrefixes}
                    onChange={e => updateField('ouiPrefixes', e.target.value)}
                    className={inputClass + ' font-mono'}
                  />
                  <p className="text-[10px] text-dark-600 mt-0.5">Comma-separated, e.g. DC:A6:32</p>
                </div>
                <div>
                  <label className={labelClass}>SSIDs</label>
                  <input
                    type="text"
                    placeholder="MyNetwork, Guest-WiFi, ..."
                    value={form.ssids}
                    onChange={e => updateField('ssids', e.target.value)}
                    className={inputClass}
                  />
                  <p className="text-[10px] text-dark-600 mt-0.5">Comma-separated</p>
                </div>
                <div>
                  <label className={labelClass}>Channels</label>
                  <input
                    type="text"
                    placeholder="1, 6, 11, 36, ..."
                    value={form.channels}
                    onChange={e => updateField('channels', e.target.value)}
                    className={inputClass + ' font-mono'}
                  />
                  <p className="text-[10px] text-dark-600 mt-0.5">Comma-separated numbers</p>
                </div>
                <div>
                  <label className={labelClass}>Min RSSI (dBm)</label>
                  <input
                    type="number"
                    min={-100}
                    max={0}
                    placeholder="-80"
                    value={form.minRssi}
                    onChange={e => updateField('minRssi', e.target.value)}
                    className={inputClass + ' font-mono'}
                  />
                  <p className="text-[10px] text-dark-600 mt-0.5">Range: -100 to 0</p>
                </div>
                <div>
                  <label className={labelClass}>Max RSSI (dBm)</label>
                  <input
                    type="number"
                    min={-100}
                    max={0}
                    placeholder="-30"
                    value={form.maxRssi}
                    onChange={e => updateField('maxRssi', e.target.value)}
                    className={inputClass + ' font-mono'}
                  />
                  <p className="text-[10px] text-dark-600 mt-0.5">Range: -100 to 0</p>
                </div>
              </div>
            </div>

            {/* Message Template */}
            <div>
              <label className={labelClass}>Message Template</label>
              <input
                type="text"
                placeholder="Device {mac} detected on {ssid} (RSSI: {rssi}) - Rule: {rule}"
                value={form.messageTemplate}
                onChange={e => updateField('messageTemplate', e.target.value)}
                className={inputClass}
              />
              <p className="text-[10px] text-dark-600 mt-0.5">
                Placeholders: {'{mac}'} {'{ssid}'} {'{rssi}'} {'{channel}'} {'{oui}'} {'{nodeId}'} {'{nodeName}'} {'{rule}'} {'{severity}'}
              </p>
            </div>

            {/* Actions */}
            <div className="flex items-center justify-between pt-2 border-t border-dark-700/50">
              <p className="text-[10px] text-dark-500">
                Match mode <span className="text-dark-300 font-medium">{form.matchMode}</span>: triggers when {form.matchMode === 'ANY' ? 'any one' : 'all'} condition{form.matchMode === 'ALL' ? 's match' : ' matches'}
              </p>
              <div className="flex gap-2">
                <button
                  onClick={() => { setShowCreate(false); setForm({ ...defaultForm }) }}
                  className="px-4 py-2 bg-dark-700 hover:bg-dark-600 text-dark-300 text-sm rounded font-medium transition-colors"
                >
                  Cancel
                </button>
                <button
                  onClick={handleCreate}
                  disabled={!form.name.trim() || createRule.isPending}
                  className="px-4 py-2 bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors"
                >
                  {createRule.isPending ? 'Creating...' : 'Create Rule'}
                </button>
              </div>
            </div>
            {createRule.isError && (
              <p className="text-sm text-red-400">{(createRule.error as Error).message}</p>
            )}
          </div>
        </div>
      )}

      {/* Alert Rules Section */}
      <div>
        <h3 className="text-sm font-medium text-dark-200 mb-3 flex items-center gap-2">
          <svg className="w-4 h-4 text-dark-400" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z" />
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
          </svg>
          Alert Rules
        </h3>
        <div className="bg-surface rounded-xl border border-dark-700/50 overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-dark-700/50 text-dark-400 text-xs uppercase tracking-wider">
                <th className="text-left px-4 py-3">Name</th>
                <th className="text-left px-4 py-3">Severity</th>
                <th className="text-center px-4 py-3">Enabled</th>
                <th className="text-right px-4 py-3">Cooldown</th>
                <th className="text-left px-4 py-3">Last Triggered</th>
                <th className="text-right px-4 py-3">Actions</th>
              </tr>
            </thead>
            <tbody>
              {rulesLoading ? (
                <tr>
                  <td colSpan={6} className="px-4 py-8 text-center text-dark-500">
                    <div className="flex items-center justify-center gap-2">
                      <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                      </svg>
                      Loading rules...
                    </div>
                  </td>
                </tr>
              ) : rules.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-8 text-center text-dark-500">
                    No alert rules configured
                  </td>
                </tr>
              ) : (
                rules.map((rule) => (
                  <tr key={rule.id} className="border-b border-dark-700/30 hover:bg-dark-800/30 transition-colors">
                    <td className="px-4 py-2.5">
                      <div className="text-dark-200 font-medium">{rule.name}</div>
                      {rule.description && (
                        <div className="text-dark-500 text-xs mt-0.5">{rule.description}</div>
                      )}
                    </td>
                    <td className="px-4 py-2.5">
                      <span className={`px-2 py-0.5 rounded-full text-xs font-medium ${severityBadge(rule.severity)}`}>
                        {rule.severity}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-center">
                      <button
                        onClick={() => toggleRule.mutate(rule)}
                        className={`relative inline-flex h-5 w-9 rounded-full transition-colors ${
                          rule.enabled ? 'bg-primary-600' : 'bg-dark-700'
                        }`}
                      >
                        <span
                          className={`inline-block w-3.5 h-3.5 rounded-full bg-white shadow transform transition-transform mt-[3px] ${
                            rule.enabled ? 'translate-x-[18px]' : 'translate-x-[3px]'
                          }`}
                        />
                      </button>
                    </td>
                    <td className="px-4 py-2.5 text-right text-dark-400 text-xs font-mono">
                      {formatCooldown(rule.cooldownSeconds)}
                    </td>
                    <td className="px-4 py-2.5 text-dark-500 text-xs">
                      {rule.lastTriggered
                        ? new Date(rule.lastTriggered).toLocaleString()
                        : 'Never'}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      <button
                        onClick={() => {
                          if (confirm(`Delete rule "${rule.name}"?`)) {
                            deleteRule.mutate(rule.id)
                          }
                        }}
                        className="text-dark-500 hover:text-status-hostile transition-colors"
                        title="Delete rule"
                      >
                        <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" d="M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0" />
                        </svg>
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Alert Events Section */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-medium text-dark-200 flex items-center gap-2">
            <svg className="w-4 h-4 text-dark-400" fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            Recent Events
            {unackedCount > 0 && (
              <span className="ml-1 px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-alert-alert/20 text-alert-alert">
                {unackedCount}
              </span>
            )}
          </h3>
          <select
            value={eventsLimit}
            onChange={(e) => setEventsLimit(Number(e.target.value))}
            className="text-xs bg-dark-800 border border-dark-700/50 text-dark-300 rounded-lg px-2 py-1 focus:outline-none focus:ring-1 focus:ring-primary-600"
          >
            <option value={25}>Last 25</option>
            <option value={50}>Last 50</option>
            <option value={100}>Last 100</option>
          </select>
        </div>

        <div className="bg-surface rounded-xl border border-dark-700/50 overflow-hidden">
          <div className="divide-y divide-dark-700/30">
            {eventsLoading ? (
              <div className="px-4 py-8 text-center text-dark-500">
                <div className="flex items-center justify-center gap-2">
                  <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                  Loading events...
                </div>
              </div>
            ) : events.length === 0 ? (
              <div className="px-4 py-8 text-center text-dark-500">
                No alert events
              </div>
            ) : (
              events.map((evt) => (
                <div
                  key={evt.id}
                  className={`flex items-start gap-3 px-4 py-3 hover:bg-dark-800/30 transition-colors ${
                    !evt.acknowledged ? 'bg-dark-800/10' : ''
                  }`}
                >
                  {/* Severity icon */}
                  <div className="mt-0.5 flex-shrink-0">
                    <svg
                      className={`w-5 h-5 ${
                        evt.severity === 'CRITICAL' ? 'text-alert-critical' :
                        evt.severity === 'ALERT' ? 'text-alert-alert' :
                        evt.severity === 'NOTICE' ? 'text-alert-notice' :
                        'text-alert-info'
                      }`}
                      fill="none" stroke="currentColor" strokeWidth={1.5} viewBox="0 0 24 24"
                    >
                      <path strokeLinecap="round" strokeLinejoin="round" d={severityIcon(evt.severity)} />
                    </svg>
                  </div>

                  {/* Content */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${severityBadge(evt.severity)}`}>
                        {evt.severity}
                      </span>
                      <span className="text-dark-200 text-sm font-medium truncate">{evt.title}</span>
                    </div>
                    {evt.message && (
                      <p className="text-dark-400 text-xs mt-1 line-clamp-2">{evt.message}</p>
                    )}
                    <div className="flex items-center gap-3 mt-1.5 text-[10px] text-dark-500">
                      <span>{new Date(evt.createdAt).toLocaleString()}</span>
                      {evt.acknowledged && (
                        <span className="text-status-friendly">
                          Acked{evt.acknowledgedBy ? ` by ${evt.acknowledgedBy}` : ''}
                        </span>
                      )}
                    </div>
                  </div>

                  {/* Acknowledge button */}
                  {!evt.acknowledged && (
                    <button
                      onClick={() => ackEvent.mutate(evt.id)}
                      className="flex-shrink-0 px-2 py-1 text-xs rounded-lg bg-dark-800 text-dark-400 hover:bg-primary-600/20 hover:text-primary-400 transition-colors border border-dark-700/50"
                      title="Acknowledge"
                    >
                      Ack
                    </button>
                  )}
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
