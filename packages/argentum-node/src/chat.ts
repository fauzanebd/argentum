import { randomUUID } from 'node:crypto';
import type { HttpClient } from './http.js';
import { readSSE } from './sse.js';
import type { ChatEvent, ChatRequest, Message, MessagePage, Thread, ThreadPage, Turn } from './types.js';

export interface SendOptions {
  signal?: AbortSignal;
  /** Supply your own so a retry after a lost response replays instead of billing a second turn. */
  idempotencyKey?: string;
}

export interface StreamOptions extends SendOptions {
  /**
   * The `id` of the last frame you saw. The messages after it are replayed
   * before the stream attaches live.
   *
   * Only `message` and `final` frames carry ids — those are the ones backed by
   * something persisted. A delta has none, because it exists nowhere but the
   * connection that carried it.
   */
  lastEventId?: string;
}

export interface PageOptions {
  limit?: number;
  cursor?: string;
  signal?: AbortSignal;
}

/** A question in, an answer out. */
export class Chat {
  readonly threads: Threads;

  constructor(private readonly http: HttpClient) {
    this.threads = new Threads(http);
  }

  /**
   * Ask, and wait for the answer on the connection.
   *
   * A turn that outruns the server's synchronous window throws
   * `WorkInProgressError` — **not** a failure. The turn is still running and
   * still being billed; the error carries `threadId`, and `chat.attach()` is
   * where you go with it. Asking again would pay for the answer twice.
   */
  async send(request: ChatRequest, options: SendOptions = {}): Promise<Turn> {
    return this.http.json<Turn>({
      method: 'POST',
      path: '/v1/chat',
      body: request,
      accept: 'application/json',
      idempotencyKey: options.idempotencyKey ?? randomUUID(),
      ...(options.signal ? { signal: options.signal } : {}),
    });
  }

  /**
   * Ask, and read the answer as it is written.
   *
   * ```ts
   * for await (const ev of client.chat.stream({ message: 'Revenue last month?', user_ref: 'u_42' })) {
   *   if (ev.event === 'delta') process.stdout.write(ev.data.content);
   *   if (ev.event === 'final') console.log('\ncost:', ev.data.usage?.cost_usd);
   * }
   * ```
   */
  async *stream(request: ChatRequest, options: StreamOptions = {}): AsyncGenerator<ChatEvent> {
    const res = await this.http.raw({
      method: 'POST',
      path: '/v1/chat',
      body: request,
      accept: 'text/event-stream',
      stream: true,
      idempotencyKey: options.idempotencyKey ?? randomUUID(),
      ...(options.lastEventId ? { headers: { 'Last-Event-ID': options.lastEventId } } : {}),
      ...(options.signal ? { signal: options.signal } : {}),
    });
    yield* chatFrames(res, options.signal);
  }

  /**
   * Attach to a thread's newest turn — the resume door.
   *
   * This is where a `WorkInProgressError` sends you, and where an SSE client
   * that lost its connection reconnects with `lastEventId`. If the turn has
   * already answered you get the answer and the stream closes.
   */
  async *attach(threadId: string, options: StreamOptions = {}): AsyncGenerator<ChatEvent> {
    const res = await this.http.raw({
      method: 'GET',
      path: `/v1/threads/${encodeURIComponent(threadId)}/events`,
      accept: 'text/event-stream',
      stream: true,
      ...(options.lastEventId ? { headers: { 'Last-Event-ID': options.lastEventId } } : {}),
      ...(options.signal ? { signal: options.signal } : {}),
    });
    yield* chatFrames(res, options.signal);
  }
}

/** The conversations this integration started. */
export class Threads {
  constructor(private readonly http: HttpClient) {}

  async list(options: PageOptions & { user_ref?: string } = {}): Promise<ThreadPage> {
    return this.http.json<ThreadPage>({
      method: 'GET',
      path: '/v1/threads',
      query: { limit: options.limit, cursor: options.cursor, user_ref: options.user_ref },
      ...(options.signal ? { signal: options.signal } : {}),
    });
  }

  /**
   * Every thread, following the cursor for you.
   *
   * It exists because the failure it prevents is silent: a caller who reads
   * `data` and forgets `has_more` sees one page and believes it is the whole
   * list.
   */
  async *listAll(options: PageOptions & { user_ref?: string } = {}): AsyncGenerator<Thread> {
    let cursor = options.cursor;
    for (;;) {
      const page = await this.list({ ...options, ...(cursor ? { cursor } : {}) });
      for (const thread of page.data) yield thread;
      if (!page.has_more || !page.next_cursor) return;
      cursor = page.next_cursor;
    }
  }

  async get(id: string, signal?: AbortSignal): Promise<Thread> {
    return this.http.json<Thread>({ method: 'GET', path: `/v1/threads/${encodeURIComponent(id)}`, ...(signal ? { signal } : {}) });
  }

  /** One page of a transcript, oldest first. */
  async messages(id: string, options: PageOptions = {}): Promise<MessagePage> {
    return this.http.json<MessagePage>({
      method: 'GET',
      path: `/v1/threads/${encodeURIComponent(id)}/messages`,
      query: { limit: options.limit, cursor: options.cursor },
      ...(options.signal ? { signal: options.signal } : {}),
    });
  }

  /** The whole transcript, following the cursor. */
  async *messagesAll(id: string, options: PageOptions = {}): AsyncGenerator<Message> {
    let cursor = options.cursor;
    for (;;) {
      const page = await this.messages(id, { ...options, ...(cursor ? { cursor } : {}) });
      for (const message of page.data) yield message;
      if (!page.has_more || !page.next_cursor) return;
      cursor = page.next_cursor;
    }
  }

  /** Delete a conversation. Needs `write:chat` — destroying one is not a read. */
  async delete(id: string, signal?: AbortSignal): Promise<void> {
    await this.http.raw({ method: 'DELETE', path: `/v1/threads/${encodeURIComponent(id)}`, ...(signal ? { signal } : {}) });
  }
}

/**
 * Turns SSE frames into typed events, and stops at the terminal one.
 *
 * Stopping matters: the server closes the connection after `final`, and a loop
 * that kept reading would hand the caller an iterator that ends silently
 * rather than one that ended *because the turn ended*.
 */
async function* chatFrames(res: Response, signal?: AbortSignal): AsyncGenerator<ChatEvent> {
  for await (const frame of readSSE(res, signal)) {
    const event = { event: frame.event, data: JSON.parse(frame.data) as never, ...(frame.id ? { id: frame.id } : {}) } as ChatEvent;
    yield event;
    if (event.event === 'final' || event.event === 'error') return;
  }
}
