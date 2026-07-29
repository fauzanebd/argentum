import type { HttpClient } from './http.js';
import type { ArgentumDocument, DocumentFormat, DocumentPage } from './types.js';

export interface ListDocumentsOptions {
  limit?: number;
  cursor?: string;
  format?: DocumentFormat;
  /** RFC3339, or a Date. */
  created_after?: string | Date;
  created_before?: string | Date;
  signal?: AbortSignal;
}

/** The files either report door produced. Read-only: there is no upload. */
export class Documents {
  constructor(private readonly http: HttpClient) {}

  async list(options: ListDocumentsOptions = {}): Promise<DocumentPage> {
    return this.http.json<DocumentPage>({
      method: 'GET',
      path: '/v1/documents',
      query: {
        limit: options.limit,
        cursor: options.cursor,
        format: options.format,
        created_after: iso(options.created_after),
        created_before: iso(options.created_before),
      },
      ...(options.signal ? { signal: options.signal } : {}),
    });
  }

  /** Every document, following the cursor for you. */
  async *listAll(options: ListDocumentsOptions = {}): AsyncGenerator<ArgentumDocument> {
    let cursor = options.cursor;
    for (;;) {
      const page = await this.list({ ...options, ...(cursor ? { cursor } : {}) });
      for (const doc of page.data) yield doc;
      if (!page.has_more || !page.next_cursor) return;
      cursor = page.next_cursor;
    }
  }

  /**
   * One document, with a `download_url` presigned at the moment you asked.
   *
   * The URL is re-issued on every read rather than stored, so one you saved an
   * hour ago is stale and this call is how you get a working one — without
   * paying to regenerate the document.
   */
  async get(id: string, signal?: AbortSignal): Promise<ArgentumDocument> {
    return this.http.json<ArgentumDocument>({ method: 'GET', path: `/v1/documents/${encodeURIComponent(id)}`, ...(signal ? { signal } : {}) });
  }

  /** The file's bytes, streamed from the API rather than from a redirect. */
  async download(id: string, signal?: AbortSignal): Promise<Buffer> {
    return this.http.bytes({
      method: 'GET',
      path: `/v1/documents/${encodeURIComponent(id)}/content`,
      accept: 'application/octet-stream',
      ...(signal ? { signal } : {}),
    });
  }
}

function iso(v: string | Date | undefined): string | undefined {
  if (v === undefined) return undefined;
  return typeof v === 'string' ? v : v.toISOString();
}
