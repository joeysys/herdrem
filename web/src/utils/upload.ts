// Uploads one file to the relay's /upload endpoint. XMLHttpRequest rather than
// fetch because only XHR reports upload progress.

/** Keep in step with service.MaxUploadBytes (internal/service/upload.go). */
export const MAX_UPLOAD_BYTES = 100 * 1024 * 1024

export interface UploadParams {
  /** relay HTTP base, e.g. https://relay.example */
  base: string
  /** relay token, sent as a Bearer header when present */
  token?: string
  host: string
  /** absolute destination directory */
  dir: string
  name: string
  file: Blob
  overwrite?: boolean
  onProgress?: (loaded: number, total: number) => void
}

export type UploadOutcome =
  | { ok: true; path: string; size: number; replaced: boolean }
  | { ok: false; status: number; message: string; aborted?: boolean }

export interface UploadHandle {
  promise: Promise<UploadOutcome>
  abort: () => void
}

export function startUpload(p: UploadParams): UploadHandle {
  const xhr = new XMLHttpRequest()
  const q = new URLSearchParams({ host: p.host, dir: p.dir, name: p.name, size: String(p.file.size) })
  if (p.overwrite) q.set('overwrite', '1')
  const promise = new Promise<UploadOutcome>((resolve) => {
    xhr.onload = () => {
      if (xhr.status === 200) {
        try {
          const r = JSON.parse(xhr.responseText) as { path: string; size: number; replaced?: boolean }
          resolve({ ok: true, path: r.path, size: r.size, replaced: !!r.replaced })
        } catch {
          resolve({ ok: false, status: xhr.status, message: 'unexpected response' })
        }
        return
      }
      resolve({ ok: false, status: xhr.status, message: (xhr.responseText || xhr.statusText || '').trim() })
    }
    xhr.onerror = () => resolve({ ok: false, status: 0, message: 'network error' })
    xhr.onabort = () => resolve({ ok: false, status: 0, message: 'cancelled', aborted: true })
  })
  xhr.open('POST', `${p.base}/upload?${q.toString()}`)
  // Required by the relay: a header no cross-site form can send.
  xhr.setRequestHeader('X-Herdrem-Upload', '1')
  xhr.setRequestHeader('Content-Type', 'application/octet-stream')
  if (p.token) xhr.setRequestHeader('Authorization', `Bearer ${p.token}`)
  xhr.upload.onprogress = (e) => p.onProgress?.(e.loaded, e.lengthComputable ? e.total : p.file.size)
  xhr.send(p.file)
  return { promise, abort: () => xhr.abort() }
}

/**
 * "report.pdf" → "report (1).pdf", "report (2).pdf", … — the first name not
 * in `taken`. Dotfiles and names without an extension get the suffix at the end.
 */
export function uniqueName(name: string, taken: ReadonlySet<string>): string {
  if (!taken.has(name)) return name
  const dot = name.lastIndexOf('.')
  const hasExt = dot > 0 && dot < name.length - 1
  const stem = hasExt ? name.slice(0, dot) : name
  const ext = hasExt ? name.slice(dot) : ''
  for (let i = 1; ; i++) {
    const candidate = `${stem} (${i})${ext}`
    if (!taken.has(candidate)) return candidate
  }
}
