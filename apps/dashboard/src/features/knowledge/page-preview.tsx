import { useQuery } from "@tanstack/react-query";
import type { Page } from "@argentum/api-types/docparse";
import type { PageBox } from "@argentum/api-types/doctable";

import { api } from "@/lib/api";

/**
 * The page a table was read from, drawn from the parser's own word boxes
 * (T-P7).
 *
 * **A reviewer who cannot see the page cannot review the parse.** That is the
 * ticket's sentence and this is the whole of what it asks for: the words where
 * they sat, and a rectangle around the grid the extraction came from.
 *
 * It is not an image of the page, and the difference is worth stating. What is
 * drawn here is *what the parser read* — so a column boundary in the wrong
 * place, or a line of type the hygiene step dropped as invisible, shows up as a
 * difference between this panel and the paper. A rendered page would look
 * righter and prove less; it would also mean rasterising every page a reviewer
 * opens, which is T-P3's machinery and its egress argument.
 */
export function PagePreview({
  documentId,
  page,
  boxes,
}: {
  documentId: string;
  page: number;
  boxes: PageBox[];
}) {
  const { data, isLoading } = useQuery({
    queryKey: ["knowledge-page", documentId, page],
    queryFn: async () =>
      (await api.get<Page>(`/knowledge/documents/${documentId}/pages/${page}`)).data,
    retry: false,
  });

  if (isLoading) {
    return <aside className="text-xs text-muted-foreground">Loading page {page}…</aside>;
  }
  if (!data) {
    return (
      <aside className="text-xs text-muted-foreground">
        Page {page} could not be read back.
      </aside>
    );
  }

  const width = data.width || 595;
  const height = data.height || 842;
  const rect = boxes.find((b) => b.page === page)?.bbox;

  return (
    <aside className="min-w-0">
      <p className="mb-1 text-xs text-muted-foreground">
        Page {page} as the parser read it
        {data.kind === "ocr" && " — read by a model, not from a text layer"}
        {data.hidden_char_count ? ` · ${data.hidden_char_count} invisible characters dropped` : ""}
      </p>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="h-auto w-full rounded border border-border bg-white"
        role="img"
        aria-label={`Page ${page}`}
      >
        {/* Every word, at 60% of its box height — small enough that a dense
            page still reads as a page, large enough to recognise a heading. */}
        {(data.words ?? []).map((w, i) => (
          <text
            key={i}
            x={w.x0}
            y={w.bottom}
            fontSize={Math.max(4, (w.bottom - w.top) * 0.95)}
            fill="#111827"
          >
            {w.text}
          </text>
        ))}
        {rect && rect.length === 4 && (
          <rect
            x={rect[0]}
            y={rect[1]}
            width={Math.max(0, rect[2] - rect[0])}
            height={Math.max(0, rect[3] - rect[1])}
            fill="rgba(37, 99, 235, 0.08)"
            stroke="rgb(37, 99, 235)"
            strokeWidth={1.5}
          />
        )}
      </svg>
      {boxes.length > 1 && (
        // A joined table has one rectangle per page, and a reviewer checking it
        // has to be shown all of them rather than the first one three times.
        <p className="mt-1 text-xs text-muted-foreground">
          This table continues on page{boxes.length > 2 ? "s" : ""}{" "}
          {boxes.slice(1).map((b) => b.page).join(", ")}.
        </p>
      )}
    </aside>
  );
}
