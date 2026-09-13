/** Stands in for `@/store/auth` under the harness's vite alias.
 *
 *  `useIsAdmin` is the whole of the member scene: the rule being photographed
 *  is which tabs a member is offered, and that function is its only input.
 *
 *  `useAuthStore` is also callable as a hook, because Settings → Team marks the
 *  signed-in admin's own row "(you)" and the access matrix tells them which
 *  agents they are refused (T-Z7). The harness is signed in as Dewi, the admin
 *  in `TEAM`; `getState` keeps answering no user, as it did for every scene
 *  before this one. */
import { harnessIsAdmin } from "./fixtures";

export interface User {
  id: string;
  email: string;
  role: string;
}

const harnessUser: User = { id: "u-dewi", email: "dewi@tokomaju.id", role: "admin" };

export function useIsAdmin(): boolean {
  return harnessIsAdmin;
}

export const useAuthStore = Object.assign(
  <T>(select: (s: { user: User | null }) => T): T => select({ user: harnessUser }),
  { getState: () => ({ user: null as User | null }) },
);
