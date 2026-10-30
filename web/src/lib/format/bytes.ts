const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']

/** A size in binary units (B to TiB); 0 B for anything below one byte. */
export function formatSize(size?: number): string {
  if (typeof size !== 'number' || !Number.isFinite(size) || size < 0) return '—'
  if (size < 1) return '0 B'
  const index = Math.min(4, Math.floor(Math.log(size) / Math.log(1024)))
  return `${(size / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index] ?? 'B'}`
}

/** A disk capacity in GiB or TiB with two decimals. */
export function formatDiskSize(bytes?: number): string {
  if (typeof bytes !== 'number' || !Number.isFinite(bytes) || bytes < 0) return '—'
  return bytes >= 1024 ** 4
    ? `${(bytes / 1024 ** 4).toFixed(2)} TiB`
    : `${(bytes / 1024 ** 3).toFixed(2)} GiB`
}
