import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { FileUp, Trash2 } from "lucide-react";
import type { SourceDocument } from "@argentum/api-types";

import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { useIsAdmin } from "@/store/auth";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useToast } from "@/hooks/use-toast";

/**
 * The PDFs this workspace has uploaded, and what happened to each (T-P1/T-P7).
 *
 * **It is `/knowledge`, not `/documents`.** That route is taken, by the
 * documents this product *generates* — and the two are opposites: one is output
 * addressed by thread, the other is input a tenant supplies. Hanging both off
 * the same noun would make every future reader disambiguate.
 *
 * Uploading and deleting are admin, reading is a member's, and a member sees
 * the controls disabled with a sentence rather than hidden — the decision
 * recorded in `docs/coverage/watchers-ui.md`: hiding a control makes somebody
 * think the feature is missing, where disabling it tells them who to ask.
 */
export function KnowledgePage() {
  const isAdmin = useIsAdmin();
  const qc = useQueryClient();
  const { toast } = useToast();
  const fileInput = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);

  const { data, isLoading } = useQuery({
    queryKey: ["knowledge-documents"],
    queryFn: async () =>
      (await api.get<{ documents: SourceDocument[] }>("/knowledge/documents"))
        .data.documents ?? [],
    // A document is parsed by a worker, so the row a tenant is looking at
    // changes without anything on this page doing it. Five seconds is slower
    // than a parse of a small report and faster than somebody's patience.
    refetchInterval: (query) =>
      (query.state.data ?? []).some((d) => d.status === "uploaded" || d.status === "parsing")
        ? 5000
        : false,
  });

  const upload = useMutation({
    mutationFn: async (file: File) => {
      const form = new FormData();
      form.append("file", file);
      return (await api.post("/knowledge/documents", form)).data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["knowledge-documents"] });
      toast({ title: "Uploaded", description: "Reading it now — this page updates itself." });
    },
    onError: (err: unknown) => {
      toast({
        title: "Upload failed",
        description: apiErrorMessage(err),
        variant: "destructive",
      });
    },
    onSettled: () => setUploading(false),
  });

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/knowledge/documents/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["knowledge-documents"] }),
    onError: (err: unknown) =>
      toast({ title: "Delete failed", description: apiErrorMessage(err), variant: "destructive" }),
  });

  const documents = data ?? [];

  return (
    <div className="p-6">
      <div className="mb-4 flex items-start justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold text-foreground">Knowledge</h1>
          <p className="mt-1 max-w-prose text-sm text-muted-foreground">
            PDFs this workspace has uploaded. A table inside one becomes data the
            agent can query — after somebody reviews what was read out of it.
          </p>
        </div>
        <div className="shrink-0">
          <input
            ref={fileInput}
            type="file"
            accept="application/pdf"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0];
              e.target.value = "";
              if (!file) return;
              setUploading(true);
              upload.mutate(file);
            }}
          />
          <Button
            onClick={() => fileInput.current?.click()}
            disabled={!isAdmin || uploading}
            title={isAdmin ? undefined : "Only an admin can upload a document — ask one of yours."}
          >
            <FileUp className="mr-2 h-4 w-4" />
            {uploading ? "Uploading…" : "Upload PDF"}
          </Button>
          {!isAdmin && (
            <p className="mt-1 text-xs text-muted-foreground">
              Only an admin can upload.
            </p>
          )}
        </div>
      </div>

      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : documents.length === 0 ? (
        <p className="max-w-prose text-sm text-muted-foreground">
          Nothing uploaded yet. A sales report, a supplier price list, a
          contract — anything whose tables or wording you would otherwise
          re-type into a spreadsheet.
        </p>
      ) : (
        <ul className="grid gap-2">
          {documents.map((doc) => (
            <li
              key={doc.id}
              className="flex items-start justify-between gap-3 rounded-lg border border-border bg-card p-3"
            >
              <Link
                to="/knowledge/$id"
                params={{ id: doc.id }}
                className="min-w-0 flex-1"
              >
                <span className="block truncate text-sm font-medium text-foreground">
                  {doc.filename}
                </span>
                <span className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                  <StatusBadge status={doc.status} />
                  <span>{doc.page_count > 0 ? `${doc.page_count} pages` : "not read yet"}</span>
                  <span>{Math.max(1, Math.round(doc.byte_size / 1024))} KB</span>
                  {doc.ocr_page_count > 0 && <span>{doc.ocr_page_count} pages read by a model</span>}
                </span>
                {/* The sentence the worker wrote onto the row. It is the
                    difference between "your document is ready" and "most of it
                    is a scan nothing has read". */}
                {doc.status_detail && (
                  <span className="mt-1 block text-xs text-muted-foreground">
                    {doc.status_detail}
                  </span>
                )}
              </Link>
              <Button
                variant="ghost"
                size="sm"
                disabled={!isAdmin || remove.isPending}
                title={
                  isAdmin
                    ? "Delete the document, its text and any table published from it"
                    : "Only an admin can delete a document."
                }
                onClick={() => remove.mutate(doc.id)}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * StatusBadge is what happened to the file, in one word.
 *
 * `failed` is destructive and `uploaded` is outline rather than both being
 * grey: a document resting at `uploaded` on a deployment with no parser is a
 * normal state, and one that failed is a sentence somebody has to read.
 */
function StatusBadge({ status }: { status: string }) {
  const variant =
    status === "failed" ? "destructive" : status === "parsed" ? "default" : "outline";
  return (
    <Badge variant={variant} className="capitalize">
      {status}
    </Badge>
  );
}
