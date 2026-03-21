import { useState } from 'react'

interface ExportItem {
  label: string
  description: string
  endpoint: string
  filename: string
  format: string
}

const exports: ExportItem[] = [
  {
    label: 'Drones',
    description: 'Export all drone detections including MAC, serial, UAS ID, position, and timestamps.',
    endpoint: '/exports/drones',
    filename: 'drones.csv',
    format: 'CSV',
  },
  {
    label: 'Nodes',
    description: 'Export all mesh nodes including ID, name, hardware model, position, and battery status.',
    endpoint: '/exports/nodes',
    filename: 'nodes.json',
    format: 'JSON',
  },
  {
    label: 'Alerts',
    description: 'Export alert events including severity, title, message, and acknowledgment status.',
    endpoint: '/exports/alerts',
    filename: 'alerts.csv',
    format: 'CSV',
  },
]

const formatBadgeColor: Record<string, string> = {
  CSV: 'bg-green-600/20 text-green-400 border-green-500/30',
  JSON: 'bg-blue-600/20 text-blue-400 border-blue-500/30',
}

export default function ExportsPage() {
  const [downloading, setDownloading] = useState<string | null>(null)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const handleDownload = async (item: ExportItem) => {
    setDownloading(item.endpoint)
    setErrorMsg(null)

    try {
      const token = localStorage.getItem('cc_token')
      const headers: Record<string, string> = {}
      if (token) {
        headers['Authorization'] = `Bearer ${token}`
      }

      const response = await fetch(`/api${item.endpoint}`, { headers })
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`)
      }

      const blob = await response.blob()
      const url = window.URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = item.filename
      document.body.appendChild(a)
      a.click()
      window.URL.revokeObjectURL(url)
      document.body.removeChild(a)
    } catch (err) {
      setErrorMsg(`Failed to export ${item.label}: ${(err as Error).message}`)
    } finally {
      setDownloading(null)
    }
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <h2 className="text-lg font-semibold text-dark-100">Data Exports</h2>
        <p className="text-sm text-dark-400 mt-1">Download system data in CSV or JSON format.</p>
      </div>

      {errorMsg && (
        <div className="mb-4 px-4 py-3 bg-red-900/20 border border-red-500/30 rounded-lg">
          <p className="text-sm text-red-400">{errorMsg}</p>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        {exports.map((item) => (
          <div
            key={item.endpoint}
            className="bg-surface rounded-lg border border-dark-700/50 p-5 flex flex-col"
          >
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-medium text-dark-200">{item.label}</h3>
              <span className={`inline-flex px-2 py-0.5 text-xs font-medium rounded border ${formatBadgeColor[item.format]}`}>
                {item.format}
              </span>
            </div>

            <p className="text-xs text-dark-400 mb-4 flex-1">{item.description}</p>

            <div className="flex items-center justify-between">
              <span className="text-xs text-dark-500 font-mono">{item.filename}</span>
              <button
                onClick={() => handleDownload(item)}
                disabled={downloading === item.endpoint}
                className="px-4 py-2 bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white text-sm rounded font-medium transition-colors"
              >
                {downloading === item.endpoint ? (
                  <span className="flex items-center gap-2">
                    <span className="inline-block w-3 h-3 border-2 border-white border-t-transparent rounded-full animate-spin" />
                    Downloading...
                  </span>
                ) : (
                  'Download'
                )}
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
