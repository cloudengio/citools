import { useEffect, useState } from 'react'
import { fmtDuration } from '../format'

// LiveDuration shows the elapsed time between `from` and `to`. While the job is
// still running (`to` is unset) it ticks once a second in the browser so the
// duration advances in real time without waiting on server updates. Once `to` is
// set the duration is fixed and no timer runs.
export function LiveDuration({ from, to }: { from?: string; to?: string }) {
  const live = !!from && !to
  const [, setTick] = useState(0)
  useEffect(() => {
    if (!live) return
    const id = window.setInterval(() => setTick((t) => t + 1), 1000)
    return () => window.clearInterval(id)
  }, [live])
  return <>{fmtDuration(from, to)}</>
}
