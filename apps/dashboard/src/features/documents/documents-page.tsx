import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Download, Link2, Trash2 } from "lucide-react";

import { api } from "@/lib/api";
import { useIsAdmin } from "@/store/auth";
import { Button } from "@/components/ui/button";
import { useToast } from "@/hooks/use-toast";

/**
 * Documents, and the links that play them (T-V4).
 *
 * The share half is admin-only on the server, and this page reflects that by
 * disabling the control rather than hiding it — the decision recorded on
 * 2026-08-04 for the watcher and approval UIs: hiding a control makes a member
 * think the feature is missing, while a disabled one tells them who to ask.
 */

type Document = {
  id: string;
  filename: string;
  format: string;
  size_bytes: number;
  source: string;
  created_at: string;
  download_url?: string;
  shareable: boolean;
};

type Share = {
  id: string;
  document_id: string;
  created_at: string;
  expires_at: string;
  revoked_at?: string;
  view_count: number;
  last_viewed_at?: string;
  live: boolean;
};

export function DocumentsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["documents"],
    queryFn: async () =>
      (await api.get<{ documents: Document[] }>("/documents")).data.documents,
  });
  const [open, setOpen] = useState<string | null>(null);

  if (isLoading) {
    return <p className="p-6 text-sm text-muted-foreground">Loading…</p>;
  }
  if (!data?.length) {
    return (
      <div className="p-6">
        <h1 className="text-2xl font-semibold tracking-tight">Documents</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Reports the agent generates appear here. Ask for one in chat — “give
          me last month’s sales as a PDF”.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Documents</h1>
        <p className="text-sm text-muted-foreground">
          Every report this workspace has generated. Share one as a link and it
          plays in a browser, with no account and no download.
        </p>
      </div>

      <div className="divide-y rounded-lg border">
        {data.map((doc) => (
          <div key={doc.id} className="p-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="min-w-0">
                <p className="truncate font-medium">{doc.filename}</p>
                <p className="text-xs text-muted-foreground">
                  {doc.format.toUpperCase()} · {formatBytes(doc.size_bytes)} ·{" "}
                  {new Date(doc.created_at).toLocaleString()}
                </p>
              </div>
              <div className="flex items-center gap-2">
                {doc.download_url ? (
                  <a
                    href={doc.download_url}
                    className="inline-flex items-center gap-2 rounded-md border px-3 py-2 text-sm hover:bg-muted"
                  >
                    <Download className="h-4 w-4" />
                    Download
                  </a>
                ) : null}
                {doc.shareable ? (
                  <Button
                    variant="outline"
                    onClick={() => setOpen(open === doc.id ? null : doc.id)}
                  >
                    <Link2 className="mr-2 h-4 w-4" />
                    Share
                  </Button>
                ) : null}
              </div>
            </div>
            {open === doc.id ? <SharePanel documentId={doc.id} /> : null}
          </div>
        ))}
      </div>
    </div>
  );
}

function SharePanel({ documentId }: { documentId: string }) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const isAdmin = useIsAdmin();
  // The token, held in memory for exactly as long as this panel is open. It is
  // never re-readable: the server stores a hash, and the list route below
  // cannot return it.
  const [minted, setMinted] = useState<{ id: string; url: string } | null>(null);
  const [copied, setCopied] = useState(false);

  const { data: shares } = useQuery({
    queryKey: ["shares", documentId],
    queryFn: async () =>
      (await api.get<{ shares: Share[] }>(`/documents/${documentId}/shares`))
        .data.shares,
    enabled: isAdmin,
  });

  const create = useMutation({
    mutationFn: async () =>
      (
        await api.post<{ share: Share; token: string }>(
          `/documents/${documentId}/shares`,
          {},
        )
      ).data,
    onSuccess: (res) => {
      setMinted({
        id: res.share.id,
        url: `${window.location.origin}/s/${res.token}`,
      });
      void qc.invalidateQueries({ queryKey: ["shares", documentId] });
    },
    onError: (err: unknown) => {
      toast({
        title: "This document cannot be shared",
        description: messageOf(err),
        variant: "destructive",
      });
    },
  });

  const revoke = useMutation({
    mutationFn: async (shareId: string) =>
      api.delete(`/documents/${documentId}/shares/${shareId}`),
    onSuccess: (_res, shareId) => {
      if (minted?.id === shareId) setMinted(null);
      void qc.invalidateQueries({ queryKey: ["shares", documentId] });
      toast({ title: "Link revoked", description: "It stops working now." });
    },
  });

  if (!isAdmin) {
    return (
      <p className="mt-4 rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">
        Sharing a report creates a link anyone can open. Ask an admin.
      </p>
    );
  }

  return (
    <div className="mt-4 space-y-4 rounded-md border bg-muted/30 p-4">
      {minted ? (
        <div className="space-y-2">
          {/* Shown exactly once. T-13's precedent for an API key, for the same
              reason: a token a UI can re-read is a token in a screenshot. */}
          <p className="text-sm font-medium">
            Copy this link now — it is not shown again.
          </p>
          <div className="flex gap-2">
            <input
              readOnly
              value={minted.url}
              className="w-full rounded-md border bg-background px-3 py-2 font-mono text-xs"
              onFocus={(e) => e.currentTarget.select()}
            />
            <Button
              variant="outline"
              onClick={() => {
                void navigator.clipboard.writeText(minted.url);
                setCopied(true);
                setTimeout(() => setCopied(false), 1500);
              }}
            >
              {copied ? (
                <Check className="h-4 w-4" />
              ) : (
                <Copy className="h-4 w-4" />
              )}
            </Button>
          </div>
        </div>
      ) : (
        <Button onClick={() => create.mutate()} disabled={create.isPending}>
          {create.isPending ? "Creating…" : "Create a share link"}
        </Button>
      )}

      <div className="space-y-2">
        <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Links
        </p>
        {!shares?.length ? (
          <p className="text-sm text-muted-foreground">None yet.</p>
        ) : (
          <ul className="space-y-1">
            {shares.map((s) => (
              <li
                key={s.id}
                className="flex flex-wrap items-center justify-between gap-2 rounded border bg-background px-3 py-2 text-sm"
              >
                <span className="text-muted-foreground">
                  {s.live ? (
                    <>
                      Expires {new Date(s.expires_at).toLocaleDateString()} ·{" "}
                      {s.view_count} view{s.view_count === 1 ? "" : "s"}
                      {s.last_viewed_at
                        ? ` · last ${new Date(s.last_viewed_at).toLocaleString()}`
                        : ""}
                    </>
                  ) : (
                    <>{s.revoked_at ? "Revoked" : "Expired"}</>
                  )}
                </span>
                {s.live ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => revoke.mutate(s.id)}
                    disabled={revoke.isPending}
                  >
                    <Trash2 className="mr-2 h-4 w-4" />
                    Revoke
                  </Button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function formatBytes(n: number) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

function messageOf(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } } };
  return e?.response?.data?.error ?? "Try again in a moment.";
}
