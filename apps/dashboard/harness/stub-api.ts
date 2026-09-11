/** Stands in for `@/lib/api` under the harness's vite alias. The scene picks
 *  which fixture set is behind it before React mounts. */
import { makeAPI, SKILLS_OK } from "./fixtures";

let impl = makeAPI(SKILLS_OK);

export function setFixtures(next: ReturnType<typeof makeAPI>) {
  impl = next;
}

export const api = {
  get: (path: string) => impl.get(path),
  post: (path: string, body?: unknown) => impl.post(path, body),
  put: (path: string, body?: unknown) => impl.put(path, body),
  delete: (path: string) => impl.delete(path),
};
