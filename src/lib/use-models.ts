import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

export type ModelRole = "primary" | "light" | "classifier";

export type ModelConfig = {
  role: ModelRole;
  model: string;
  interface: string;
  input_per_1k_usd: number;
  output_per_1k_usd: number;
  pricing_known: boolean;
};

export type ModelsResponse = {
  primary: ModelConfig;
  light: ModelConfig;
  classifier: ModelConfig;
};

export function useModels() {
  return useQuery({
    queryKey: ["config", "models"],
    queryFn: async () => (await api.get<ModelsResponse>("/config/models")).data,
    staleTime: 5 * 60 * 1000,
  });
}
