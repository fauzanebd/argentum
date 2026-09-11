/** Types for `constants.js`.
 *
 *  The constants are plain JS so that node (`shoot.mjs`) and vite
 *  (`fixtures.tsx`) can import the same literal without a build step between
 *  them; this file is what lets `tsc` see them too, since `allowJs` is off for
 *  the app project the harness is typechecked under.
 */
export const FORM_NAME: string;
export const FORM_TRIGGER: string;
export const FORM_BODY: string;
