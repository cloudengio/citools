// Badge renders a small coloured status pill. The kind drives the CSS colour
// class (see styles.css) and defaults to a neutral style for unknown values.
export function Badge({ value }: { value?: string }) {
  const kind = (value ?? 'unknown').toLowerCase()
  return <span className={`badge badge-${kind}`}>{value ?? 'unknown'}</span>
}
