import { useState, useEffect } from 'react'
import { getServiceStatus, restartService, uninstallService, type ServiceStatus } from '../api/client'

export function ServiceControls() {
  const [service, setService] = useState<ServiceStatus | null>(null)
  const [restarting, setRestarting] = useState(false)
  const [uninstalled, setUninstalled] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showConfirm, setShowConfirm] = useState<'restart' | 'uninstall' | null>(null)

  useEffect(() => {
    getServiceStatus()
      .then(setService)
      .catch(() => setService(null))
  }, [])

  const handleRestart = async () => {
    setShowConfirm(null)
    setBusy(true)
    setError(null)
    try {
      await restartService()
      setRestarting(true)
      // Poll until backend is back online
      const poll = async () => {
        for (let i = 0; i < 30; i++) {
          await new Promise((r) => setTimeout(r, 1000))
          try {
            const resp = await fetch('/api/v1/config')
            if (resp.ok) {
              window.location.reload()
              return
            }
          } catch {
            /* waiting */
          }
        }
        setRestarting(false)
        setBusy(false)
        setError('Restart timed out or server did not come back up.')
      }
      void poll()
    } catch (err) {
      setBusy(false)
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const handleUninstall = async () => {
    setShowConfirm(null)
    setBusy(true)
    setError(null)
    try {
      await uninstallService()
      setUninstalled(true)
      setService((prev) => (prev ? { ...prev, installed: false, running: false } : null))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (restarting) {
    return (
      <span className="service-alert">
        <span className="dot down" /> Restarting orchestrator service...
      </span>
    )
  }

  if (uninstalled) {
    return (
      <span className="chip sm" style={{ color: 'var(--muted)' }}>
        Service uninstalled
      </span>
    )
  }

  if (!service?.installed) {
    return null
  }

  return (
    <div className="service-controls">
      <span className="chip sm" title={`Service: ${service.label || 'installed'}`}>
        Service: Active
      </span>
      <button
        className="btn"
        disabled={busy}
        onClick={() => setShowConfirm('restart')}
        title="Restart the login service"
      >
        Restart
      </button>
      <button
        className="btn btn-danger sm"
        disabled={busy}
        onClick={() => setShowConfirm('uninstall')}
        title="Uninstall the login service"
      >
        Uninstall
      </button>

      {error && <span className="err-text" style={{ marginLeft: 6 }}>{error}</span>}

      {showConfirm === 'restart' && (
        <div className="modal-backdrop">
          <div className="modal">
            <h3>Restart Service</h3>
            <p>
              Are you sure you want to restart the GitHub Runner Orchestrator service?
              Active workflow jobs may be interrupted. The web page will automatically reconnect once restarted.
            </p>
            <div className="modal-actions">
              <button className="btn" onClick={() => setShowConfirm(null)}>Cancel</button>
              <button className="btn btn-primary" onClick={() => void handleRestart()}>Confirm Restart</button>
            </div>
          </div>
        </div>
      )}

      {showConfirm === 'uninstall' && (
        <div className="modal-backdrop">
          <div className="modal">
            <h3>Uninstall Service</h3>
            <p>
              Are you sure you want to stop and uninstall the GitHub Runner Orchestrator login service?
              This will remove the LaunchAgent and stop the orchestrator.
            </p>
            <div className="modal-actions">
              <button className="btn" onClick={() => setShowConfirm(null)}>Cancel</button>
              <button className="btn btn-danger" onClick={() => void handleUninstall()}>Confirm Uninstall</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
