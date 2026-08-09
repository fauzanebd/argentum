import { registerRoot } from "remotion";

import { RemotionRoot } from "./composition";

/**
 * The Remotion entry point, and nothing else.
 *
 * `registerRoot` has a side effect the moment the module is evaluated, so it
 * lives in a file that only a bundler entry point imports. The dashboard's
 * player (T-V4) imports the composition and the components directly; if this
 * call sat in the barrel, opening a report in the dashboard would register a
 * Remotion root in a page that has no studio to register it with.
 */
registerRoot(RemotionRoot);
