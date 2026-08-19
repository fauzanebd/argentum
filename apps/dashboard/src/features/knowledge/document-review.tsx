import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ArrowLeft, CheckCircle2, ShieldAlert, TriangleAlert } from "lucide-react";
import type { DocumentTable, SourceDocument } from "@argentum/api-types";
import type { Column, Table } from "@argentum/api-types/doctable";

import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { useIsAdmin } from "@/store/auth";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useToast } from "@/hooks/use-toast";
import { PagePreview } from "./page-preview";

/** A draft is the stored decision plus the extraction it applies to. */
type Draft = DocumentTable & { table: Table };

/**
 * What was found inside one document, and the button that turns it into data
 * (T-P7).
 *
 * **This surface is the gate, not polish.** Decision 3 of the PDF roadmap: an
 * extraction that silently became the agent's view of the business would be a
 * fabrication with a UI — the same hazard `SourceProfile` refused to accept for
 * something far less load-bearing than data. So every table arrives as a draft,
 * the page it came from is on screen beside it, and a table whose own totals do
 * not add up cannot be applied at all.
 */
export function DocumentReviewPage() {
  const { id } = useParams({ from: "/protected/knowledge/$id" });
  const isAdmin = useIsAdmin();
  const qc = useQueryClient();
  const { toast } = useToast();

  const { data: doc } = useQuery({
    queryKey: ["knowledge-document", id],
    queryFn: async () => (await api.get<SourceDocument>(`/knowledge/documents/${id}`)).data,
  });

  const { data, isLoading, error } = useQuery({
    queryKey: ["knowledge-tables", id],
    queryFn: async () =>
      (await api.get<{ tables: Draft[] }>(`/knowledge/documents/${id}/tables`)).data.tables ?? [],
    retry: false,
  });

  const apply = useMutation({
    mutationFn: async (tableId: string) =>
      (await api.post<Draft>(`/knowledge/tables/${tableId}/apply`)).data,
    onSuccess: (t) => {
      qc.invalidateQueries({ queryKey: ["knowledge-tables", id] });
      toast({
        title: "Published",
        description: `${t.row_count} rows are now queryable as ${t.table_name}.`,
      });
    },
    onError: (err: unknown) =>
      toast({ title: "Not published", description: apiErrorMessage(err), variant: "destructive" }),
  });

  const unpublish = useMutation({
    mutationFn: async (tableId: string) => api.post(`/knowledge/tables/${tableId}/unpublish`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["knowledge-tables", id] }),
    onError: (err: unknown) =>
      toast({ title: "Not withdrawn", description: apiErrorMessage(err), variant: "destructive" }),
  });

  const save = useMutation({
    mutationFn: async (input: { tableId: string; title: string; columns: Column[] }) =>
      (
        await api.patch<Draft>(`/knowledge/tables/${input.tableId}`, {
          title: input.title,
          columns: input.columns,
        })
      ).data,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["knowledge-tables", id] }),
    onError: (err: unknown) =>
      toast({ title: "Not saved", description: apiErrorMessage(err), variant: "destructive" }),
  });

  return (
    <div className="p-6">
      <Link
        to="/knowledge"
        className="mb-4 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Knowledge
      </Link>

      <h1 className="text-lg font-semibold text-foreground">{doc?.filename ?? "Document"}</h1>
      {doc?.status_detail && (
        <p className="mt-1 max-w-prose text-sm text-muted-foreground">{doc.status_detail}</p>
      )}

      {isLoading && <p className="mt-6 text-sm text-muted-foreground">Reading the parse…</p>}
      {error && (
        <p className="mt-6 max-w-prose text-sm text-muted-foreground">
          {apiErrorMessage(error)}
        </p>
      )}

      <div className="mt-6 grid gap-6">
        {(data ?? []).map((draft) => (
          <TableCard
            key={draft.id}
            documentId={id}
            draft={draft}
            isAdmin={isAdmin}
            busy={apply.isPending || save.isPending || unpublish.isPending}
            onSave={(title, columns) => save.mutate({ tableId: draft.id, title, columns })}
            onApply={() => apply.mutate(draft.id)}
            onUnpublish={() => unpublish.mutate(draft.id)}
          />
        ))}
        {data && data.length === 0 && (
          <p className="max-w-prose text-sm text-muted-foreground">
            No tables were found in this document. Its wording is still
            searchable — ask about it in chat.
          </p>
        )}
      </div>
    </div>
  );
}

function TableCard({
  documentId,
  draft,
  isAdmin,
  busy,
  onSave,
  onApply,
  onUnpublish,
}: {
  documentId: string;
  draft: Draft;
  isAdmin: boolean;
  busy: boolean;
  onSave: (title: string, columns: Column[]) => void;
  onApply: () => void;
  onUnpublish: () => void;
}) {
  const [columns, setColumns] = useState<Column[]>(draft.table.columns ?? []);
  const [title, setTitle] = useState(draft.title);
  const applied = draft.status === "applied";
  const quarantined = draft.verify_status === "quarantined";

  // The preview is recomputed here rather than round-tripped, so a reviewer
  // sees the effect of a type or multiplier change *before* deciding to save
  // it. The server recomputes the same values on save — this is a preview, and
  // the values that reach the warehouse are always the server's.
  const preview = useMemo(
    () => previewRows(draft.table, columns),
    [draft.table, columns],
  );

  const dirty =
    title !== draft.title || JSON.stringify(columns) !== JSON.stringify(draft.table.columns ?? []);

  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="flex flex-wrap items-start justify-between gap-3 border-b border-border p-4">
        <div className="min-w-0">
          <input
            className="w-full max-w-md rounded border border-border bg-background px-2 py-1 text-sm font-medium text-foreground"
            value={title}
            disabled={!isAdmin || applied}
            onChange={(e) => setTitle(e.target.value)}
          />
          <p className="mt-1 text-xs text-muted-foreground">
            pages {draft.first_page}–{draft.last_page} · {draft.table.rows?.length ?? 0} rows ·
            found by {draft.table.strategy === "lines" ? "ruling lines" : "word alignment"} ·
            table name <code className="font-mono">{draft.table_name}</code>
          </p>
        </div>
        <VerifyBadge status={draft.verify_status} detail={draft.verify_detail} />
      </header>

      {/* What this package did that a reviewer would not otherwise see: a
          caption dropped, three pages joined, a column forced to text by one
          cell. */}
      {draft.table.notes && draft.table.notes.length > 0 && (
        <ul className="border-b border-border px-4 py-2 text-xs text-muted-foreground">
          {draft.table.notes.map((note, i) => (
            <li key={i}>· {note}</li>
          ))}
        </ul>
      )}

      <div className="grid gap-4 p-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
        <div className="min-w-0 overflow-x-auto">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr>
                {columns.map((col, i) => (
                  <th key={i} className="border border-border p-2 text-left align-top">
                    <span className="block truncate text-xs font-semibold text-foreground">
                      {col.header || col.name}
                    </span>
                    <ColumnControls
                      column={col}
                      disabled={!isAdmin || applied}
                      onChange={(next) =>
                        setColumns((prev) => prev.map((c, j) => (j === i ? next : c)))
                      }
                    />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {preview.map((row, r) => (
                <tr key={r}>
                  {row.map((cell, c) => (
                    <td key={c} className="border border-border p-2 align-top">
                      <span className="block text-foreground">{cell.raw}</span>
                      {cell.value !== undefined && cell.value !== cell.raw && (
                        // The value that would be stored, when it differs from
                        // what the page printed. An applied multiplier nobody
                        // can see is unauditable, which is the whole reason the
                        // multiplier is recorded on the column.
                        <span className="block text-xs text-muted-foreground">→ {cell.value}</span>
                      )}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          {draft.table.totals && draft.table.totals.length > 0 && (
            <p className="mt-2 text-xs text-muted-foreground">
              {draft.table.totals.length} total row(s) were held out of the data:
              a TOTAL loaded as data double-counts every sum built on it.
            </p>
          )}
        </div>

        <PagePreview
          documentId={documentId}
          page={draft.first_page}
          boxes={draft.table.boxes ?? []}
        />
      </div>

      <footer className="flex flex-wrap items-center justify-end gap-2 border-t border-border p-4">
        {quarantined && (
          <p className="mr-auto max-w-prose text-xs text-destructive">
            {draft.verify_detail || "This table does not add up."} It cannot be
            published until the parse is right — the figure the document states
            and the figure its own rows add up to disagree, and either one of
            them could be the wrong one.
          </p>
        )}
        {!isAdmin && (
          <p className="mr-auto text-xs text-muted-foreground">
            Only an admin can publish a table — ask one of yours.
          </p>
        )}
        {dirty && !applied && (
          <Button variant="outline" disabled={!isAdmin || busy} onClick={() => onSave(title, columns)}>
            Save changes
          </Button>
        )}
        {applied ? (
          <Button variant="outline" disabled={!isAdmin || busy} onClick={onUnpublish}>
            Withdraw
          </Button>
        ) : (
          <Button
            disabled={!isAdmin || busy || quarantined || dirty}
            title={
              quarantined
                ? "This table did not add up and cannot be published."
                : dirty
                  ? "Save the column changes first."
                  : undefined
            }
            onClick={onApply}
          >
            Apply
          </Button>
        )}
      </footer>
    </section>
  );
}

/** ColumnControls is the reviewer's override: what this column is, and by how
 * much its values were scaled. */
function ColumnControls({
  column,
  disabled,
  onChange,
}: {
  column: Column;
  disabled: boolean;
  onChange: (next: Column) => void;
}) {
  return (
    <span className="mt-1 flex flex-col gap-1">
      <select
        className="rounded border border-border bg-background px-1 py-0.5 text-xs"
        value={column.type}
        disabled={disabled}
        onChange={(e) => onChange({ ...column, type: e.target.value as Column["type"] })}
      >
        {["text", "integer", "decimal", "currency", "percentage", "date"].map((t) => (
          <option key={t} value={t}>
            {t}
          </option>
        ))}
      </select>
      <select
        className="rounded border border-border bg-background px-1 py-0.5 text-xs"
        value={String(column.multiplier ?? 1)}
        disabled={disabled}
        onChange={(e) => onChange({ ...column, multiplier: Number(e.target.value) })}
      >
        <option value="1">×1</option>
        <option value="1000">×1 thousand</option>
        <option value="1000000">×1 million</option>
        <option value="1000000000">×1 billion</option>
      </select>
      {column.multiplier_source && (
        <span className="text-[10px] text-muted-foreground" title={column.multiplier_source}>
          from “{column.multiplier_source}”
        </span>
      )}
      {column.pii && (
        <span className="inline-flex items-center gap-1 text-[10px] text-amber-600 dark:text-amber-400">
          <ShieldAlert className="h-3 w-3" />
          {column.pii === "identity" ? "identity numbers" : "contact details"}
        </span>
      )}
    </span>
  );
}

function VerifyBadge({ status, detail }: { status: string; detail?: string }) {
  if (status === "verified") {
    return (
      <Badge variant="default" className="gap-1">
        <CheckCircle2 className="h-3 w-3" /> adds up
      </Badge>
    );
  }
  if (status === "quarantined") {
    return (
      <Badge variant="destructive" className="gap-1" title={detail}>
        <TriangleAlert className="h-3 w-3" /> does not add up
      </Badge>
    );
  }
  return (
    <Badge variant="outline" title="This table states no total, so there was nothing to check against.">
      no total to check
    </Badge>
  );
}

type PreviewCell = { raw: string; value?: string };

/**
 * previewRows applies the reviewer's current column choices to the extracted
 * cells, the way the server will.
 *
 * It re-parses rather than reusing the server's `num`, because the point of the
 * preview is to show the effect of a change the server has not seen yet. The
 * parsing is deliberately simpler than the Go side's — no footnote markers, no
 * accounting brackets — and it never decides anything: what reaches the
 * warehouse is always what the server computed on save.
 */
function previewRows(table: Table, columns: Column[]): PreviewCell[][] {
  const rows = table.rows ?? [];
  return rows.slice(0, 12).map((row) =>
    (row.cells ?? []).map((cell, i) => {
      const col = columns[i];
      const raw = cell.raw ?? "";
      if (!col || col.type === "text" || col.type === "date" || raw.trim() === "") {
        return { raw };
      }
      const parsed = parseLoose(raw);
      if (parsed === null) return { raw };
      const scaled = parsed * (col.multiplier || 1);
      return { raw, value: scaled.toLocaleString("en-US", { maximumFractionDigits: 4 }) };
    }),
  );
}

/** parseLoose reads a figure in either separator convention, structurally — the
 * browser-side echo of `internal/numparse`. */
function parseLoose(raw: string): number | null {
  const s = raw.replace(/[^\d.,-]/g, "").replace(/^-|-$/g, (m) => m);
  if (!s) return null;
  const dots = (s.match(/\./g) ?? []).length;
  const commas = (s.match(/,/g) ?? []).length;
  let normalized = s;
  if (dots > 0 && commas > 0) {
    normalized =
      s.lastIndexOf(".") > s.lastIndexOf(",")
        ? s.replace(/,/g, "")
        : s.replace(/\./g, "").replace(",", ".");
  } else if (dots > 1) {
    normalized = s.replace(/\./g, "");
  } else if (commas > 1) {
    normalized = s.replace(/,/g, "");
  } else if (dots === 1 || commas === 1) {
    const sep = dots === 1 ? "." : ",";
    const at = s.indexOf(sep);
    const before = s.slice(0, at);
    const after = s.slice(at + 1);
    normalized =
      after.length === 3 && before.length >= 1 && before.length <= 3
        ? before + after
        : `${before}.${after}`;
  }
  const value = Number(normalized);
  return Number.isFinite(value) ? value : null;
}
