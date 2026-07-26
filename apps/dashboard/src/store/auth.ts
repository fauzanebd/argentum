import { create } from "zustand";
import { persist } from "zustand/middleware";

export interface User {
  id: string;
  company_id: string;
  email: string;
  role: string;
  name?: string;
  avatar?: string;
  company_name?: string;
}

interface AuthState {
  accessToken: string | null;
  user: User | null;
  setAccessToken: (token: string | null) => void;
  setUser: (user: User | null) => void;
  setSession: (token: string, user: User) => void;
  clear: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      accessToken: null,
      user: null,
      setAccessToken: (accessToken) => set({ accessToken }),
      setUser: (user) => set({ user }),
      setSession: (accessToken, user) => set({ accessToken, user }),
      clear: () => set({ accessToken: null, user: null }),
    }),
    { name: "argentum-auth" },
  ),
);
