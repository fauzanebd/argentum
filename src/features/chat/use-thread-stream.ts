import { useEffect, useRef, useState } from "react";
import type { ChatEvent } from "./types";
import { useAuthStore } from "@/store/auth";

// useThreadStream subscribes to the WebSocket for a single thread. Reconnects
// with exponential backoff on disconnection (capped at 10s).
export function useThreadStream(threadId: string | null, onEvent: (e: ChatEvent) => void) {
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (!threadId) return;
    const token = useAuthStore.getState().accessToken;
    if (!token) return;

    let backoff = 500;
    let closed = false;
    let ws: WebSocket | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const connect = () => {
      const url = `wss://argentum-api.gaia.smartsoft.co.id/api/threads/${threadId}/stream?at=${encodeURIComponent(token)}`;
      ws = new WebSocket(url);

      ws.onopen = () => {
        setConnected(true);
        backoff = 500;
      };
      ws.onmessage = (msg) => {
        try {
          const evt = JSON.parse(msg.data) as ChatEvent;
          onEventRef.current(evt);
        } catch (err) {
          console.warn("ws parse failed", err);
        }
      };
      ws.onclose = () => {
        setConnected(false);
        if (closed) return;
        reconnectTimer = setTimeout(connect, backoff);
        backoff = Math.min(backoff * 2, 10_000);
      };
      ws.onerror = () => {
        ws?.close();
      };
    };

    connect();
    return () => {
      closed = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      ws?.close();
    };
  }, [threadId]);

  return { connected };
}
