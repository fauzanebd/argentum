import DOMPurify from "dompurify";
import { marked } from "marked";

// Model output rendered as HTML, which is a sentence worth being nervous about.
//
// The agent's answer is text an LLM produced from a tenant's own data, and both
// halves of that can carry markup: a row in a warehouse can contain
// `<img onerror=…>` as easily as a product name. So the pipeline is parse →
// sanitise → insert, never parse → insert.

marked.setOptions({ breaks: true, gfm: true });

/** Links open in a new tab and carry `noopener`.
 *
 *  `noopener` is the load-bearing half: without it, a page the agent linked to
 *  gets a handle on `window.opener` and can navigate the frame that opened it.
 *  DOMPurify runs after this hook, so a hostile `target` cannot survive it
 *  either. */
DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "A") {
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer");
  }
});

/** The tags an answer may use. Everything else is stripped rather than
 *  escaped, so a stray `<script>` leaves no visible artefact. No `img`: an
 *  answer has no reason to load a remote image, and one that did would leak the
 *  reader's address to whoever the URL points at. */
const ALLOWED_TAGS = [
  "p", "br", "strong", "em", "del", "code", "pre", "blockquote",
  "ul", "ol", "li", "a", "h1", "h2", "h3", "h4", "table", "thead",
  "tbody", "tr", "th", "td", "hr",
];

export function renderMarkdown(source: string): string {
  const html = marked.parse(source, { async: false }) as string;
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS,
    ALLOWED_ATTR: ["href", "title", "target", "rel"],
    // No `data:` or `javascript:` — an anchor is for http(s) and mailto.
    ALLOWED_URI_REGEXP: /^(?:https?:|mailto:)/i,
  });
}
