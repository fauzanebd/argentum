import axios, { AxiosError, type InternalAxiosRequestConfig } from "axios";
import { useAuthStore } from "@/store/auth";

// API base URL is empty by default so requests hit the same origin (Vite
// proxies /api → :8080 in dev, prod serves dashboard from same host).
export const api = axios.create({
  baseURL: "/api",
  withCredentials: true,
});

api.interceptors.request.use((config: InternalAxiosRequestConfig) => {
  const token = useAuthStore.getState().accessToken;
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

let refreshing: Promise<string | null> | null = null;

async function refresh(): Promise<string | null> {
  if (!refreshing) {
    refreshing = (async () => {
      try {
        const res = await axios.post(
          "/api/auth/refresh",
          {},
          { withCredentials: true },
        );
        const token: string = res.data.access_token;
        useAuthStore.getState().setAccessToken(token);
        return token;
      } catch {
        useAuthStore.getState().clear();
        return null;
      } finally {
        refreshing = null;
      }
    })();
  }
  return refreshing;
}

api.interceptors.response.use(
  (r) => r,
  async (error: AxiosError) => {
    const original = error.config as InternalAxiosRequestConfig & { _retry?: boolean };
    if (
      error.response?.status === 401 &&
      original &&
      !original._retry &&
      !original.url?.includes("/auth/")
    ) {
      original._retry = true;
      const token = await refresh();
      if (token) {
        original.headers.Authorization = `Bearer ${token}`;
        return api(original);
      }
    }
    return Promise.reject(error);
  },
);
