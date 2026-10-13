import { useQuery } from "@tanstack/react-query";
import type { ModelInfo, ModelRole, ModelsResponse } from "@argentum/api-types";
import { api } from "@/lib/api";

// Generated since RIZ-97 (`handlers/wire.go`), which made `primary` the
// workspace's resolved model and added the optional `chat_model` block. The
// old names stay as aliases so the screens reading this keep their imports.
export type { ModelRole, ModelsResponse };
export type ModelConfig = ModelInfo;

/** The query key the chat model picker invalidates after a save, so the chat
 *  header reports the new model without a reload. */
export const MODELS_QUERY_KEY = ["config", "models"] as const;

export function useModels() {
  return useQuery({
    queryKey: MODELS_QUERY_KEY,
    queryFn: async () => (await api.get<ModelsResponse>("/config/models")).data,
    staleTime: 5 * 60 * 1000,
  });
}
