import { useQuery } from "@tanstack/react-query";
import type { MyCapabilitiesResponse } from "@argentum/api-types";
import { api } from "@/lib/api";
import { voiceFrom, type Voice } from "./voice";

/** Shared by every surface that reads the caller's own capabilities. */
export const MY_CAPABILITIES_KEY = ["users", "me", "capabilities"] as const;

/**
 * What this deployment and this person can do with voice (T-W9).
 *
 * One read for the microphone on both composers and the play button on every
 * answer: the cache holds it, so a transcript of forty answers asks once. A
 * minute stale, because a grant or a revoke is rare and the server refuses a
 * revoked person on their very next request whatever this screen still shows.
 */
export function useVoice(): Voice {
  const { data } = useQuery({
    queryKey: MY_CAPABILITIES_KEY,
    queryFn: async () => (await api.get<MyCapabilitiesResponse>("/users/me/capabilities")).data,
    staleTime: 60_000,
  });
  return voiceFrom(data);
}
