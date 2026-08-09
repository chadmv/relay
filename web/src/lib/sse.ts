// Pure, incremental Server-Sent Events frame parser. No network, no auth, no
// React: it is fed strings and returns frames. Auth and the 401 notifier live in
// api.ts (apiStream) so the bearer token stays in exactly one place.
//
// relay's server writes one clean `event: <type>\ndata: <json>\n\n` per flush
// (internal/api/events.go:90-92) and json.Marshal escapes newlines, so in
// practice every frame is two lines. The parser still handles a frame split
// across two reader chunks, CRLF, multi-line `data:` and comment lines, because
// a ReadableStream chunk boundary is a property of the transport, not of the
// server.

export interface SseFrame {
  /** The `event:` field value; 'message' when the frame carried none (SSE default). */
  event: string
  /** The `data:` field value; multiple data lines are joined with '\n'. */
  data: string
}

export interface SseParser {
  /** Feeds a decoded chunk and returns every frame completed by it (possibly none). */
  push(chunk: string): SseFrame[]
}

export function createSseParser(): SseParser {
  // Everything after the last '\n' seen so far. A lone trailing '\r' stays here
  // too, so a CRLF split across two chunks is joined rather than mis-parsed.
  let buf = ''
  let eventType = ''
  let dataLines: string[] = []

  return {
    push(chunk: string): SseFrame[] {
      const frames: SseFrame[] = []
      buf += chunk

      let nl = buf.indexOf('\n')
      while (nl !== -1) {
        let line = buf.slice(0, nl)
        buf = buf.slice(nl + 1)
        if (line.endsWith('\r')) line = line.slice(0, -1)

        if (line === '') {
          // Blank line = end of frame. A blank line with nothing pending (which
          // is what a `:keepalive` comment leaves behind) emits nothing.
          if (dataLines.length > 0 || eventType !== '') {
            frames.push({ event: eventType || 'message', data: dataLines.join('\n') })
          }
          eventType = ''
          dataLines = []
        } else if (!line.startsWith(':')) {
          const colon = line.indexOf(':')
          const field = colon === -1 ? line : line.slice(0, colon)
          let value = colon === -1 ? '' : line.slice(colon + 1)
          if (value.startsWith(' ')) value = value.slice(1)
          if (field === 'event') eventType = value
          else if (field === 'data') dataLines.push(value)
          // `id` and `retry` are recognised and ignored: relay sends neither and
          // does not honour Last-Event-ID (README.md:1349-1350).
        }

        nl = buf.indexOf('\n')
      }

      return frames
    },
  }
}
