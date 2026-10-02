import { useEffect, useRef, useState } from 'react';
import { SystemEvent } from '../types/api';

type EventHandler = (event: SystemEvent) => void;

/**
 * Custom hook to listen to real-time server-sent events from /api/events (LOG-4, UI-5).
 * Updates frontend within 1 second of pipeline events without page reload.
 */
export function useSSE(onEvent?: EventHandler) {
  const [isConnected, setIsConnected] = useState(false);
  const handlerRef = useRef(onEvent);
  handlerRef.current = onEvent;

  useEffect(() => {
    let es: EventSource | null = null;
    let reconnectTimeout: ReturnType<typeof setTimeout>;

    function connect() {
      // EventSource sends cookies automatically if same-origin
      es = new EventSource('/api/events');

      es.onopen = () => {
        setIsConnected(true);
      };

      es.onmessage = (e) => {
        try {
          const payload = JSON.parse(e.data);
          const sysEvent: SystemEvent = {
            id: Number(e.lastEventId) || Date.now(),
            job_id: payload.job_id || 0,
            type: e.type || 'message',
            payload: e.data,
            created_at: new Date().toISOString(),
          };
          if (handlerRef.current) {
            handlerRef.current(sysEvent);
          }
        } catch {
          // Non-JSON message or comment
        }
      };

      // Listen for specific event types if dispatched with event names
      const knownEvents = [
        'job.created',
        'job.stage_changed',
        'job.status_changed',
        'step.started',
        'step.finished',
        'approval.recorded',
      ];

      knownEvents.forEach((evType) => {
        es?.addEventListener(evType, (e: MessageEvent) => {
          try {
            const payload = JSON.parse(e.data);
            const sysEvent: SystemEvent = {
              id: Number(e.lastEventId) || Date.now(),
              job_id: payload.job_id || 0,
              type: evType,
              payload: e.data,
              created_at: new Date().toISOString(),
            };
            if (handlerRef.current) {
              handlerRef.current(sysEvent);
            }
          } catch {
            // ignore
          }
        });
      });

      es.onerror = () => {
        setIsConnected(false);
        es?.close();
        // Reconnect after 3 seconds on drop
        reconnectTimeout = setTimeout(connect, 3000);
      };
    }

    connect();

    return () => {
      setIsConnected(false);
      es?.close();
      clearTimeout(reconnectTimeout);
    };
  }, []);

  return { isConnected };
}
