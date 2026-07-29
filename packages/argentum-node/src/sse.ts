/**
 * A server-sent events reader.
 *
 * Written rather than depended on, for the reason the whole package has no
 * runtime dependencies: SSE is a line-oriented format with four field names,
 * and a client that pulls in a parser for it has taken on a supply chain to
 * avoid sixty lines.
 *
 * What it does that a naive split on `\n\n` does not:
 *
 * - reassembles frames across chunk boundaries, because a network read has no
 *   relationship to a frame boundary;
 * - keeps the last `id:` it saw, which is what a caller sends back as
 *   `Last-Event-ID` to resume;
 * - drops comments (`: heartbeat`) silently, which is what makes the keepalive
 *   cost an integrator no code.
 */
export interface SSEFrame {
  event: string;
  data: string;
  id?: string;
}

/** Parses one HTTP response body into frames, in order. */
export async function* readSSE(res: Response, signal?: AbortSignal): AsyncGenerator<SSEFrame> {
  if (!res.body) return;
  const decoder = new TextDecoder();
  let buffer = '';

  // @ts-expect-error — Node's ReadableStream is async-iterable at runtime from
  // v18, but the DOM lib this package compiles against does not declare it.
  for await (const chunk of res.body) {
    if (signal?.aborted) return;
    buffer += decoder.decode(chunk as Uint8Array, { stream: true });

    let split: number;
    // `\n\n` only: this API writes `\n` line endings, and accepting `\r\n\r\n`
    // as well would mean handling the mixed case, which nothing produces.
    while ((split = buffer.indexOf('\n\n')) !== -1) {
      const raw = buffer.slice(0, split);
      buffer = buffer.slice(split + 2);
      const frame = parseFrame(raw);
      if (frame) yield frame;
    }
  }
}

function parseFrame(raw: string): SSEFrame | undefined {
  let event = 'message';
  let id: string | undefined;
  const data: string[] = [];

  for (const line of raw.split('\n')) {
    if (line === '' || line.startsWith(':')) continue; // a comment, i.e. the heartbeat
    const colon = line.indexOf(':');
    const field = colon === -1 ? line : line.slice(0, colon);
    // One optional space after the colon is part of the format, not part of
    // the value — a JSON payload that lost its first character would fail to
    // parse for a reason nobody would find quickly.
    const value = colon === -1 ? '' : line.slice(colon + 1).replace(/^ /, '');
    switch (field) {
      case 'event':
        event = value;
        break;
      case 'data':
        data.push(value);
        break;
      case 'id':
        id = value;
        break;
      default:
        break; // `retry:` and anything we do not know
    }
  }

  if (data.length === 0 && event === 'message') return undefined;
  return { event, data: data.join('\n'), ...(id === undefined ? {} : { id }) };
}
