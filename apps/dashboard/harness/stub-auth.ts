/** Stands in for `@/store/auth` under the harness's vite alias.
 *
 *  Only `useIsAdmin` matters here, and it is the whole of the member scene: the
 *  rule being photographed is which tabs a member is offered, and that function
 *  is its only input. */
import { harnessIsAdmin } from "./fixtures";

export interface User {
  id: string;
  email: string;
  role: string;
}

export function useIsAdmin(): boolean {
  return harnessIsAdmin;
}

export const useAuthStore = {
  getState: () => ({ user: null as User | null }),
};
