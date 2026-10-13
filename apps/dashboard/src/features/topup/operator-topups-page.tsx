import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  OperatorTopup,
  OperatorTopupsResponse,
  TopupApproveResponse,
  TopupProofLinkResponse,
} from "@argentum/api-types";

import { api } from "@/lib/api";
import { apiErrorMessage, apiErrorStatus } from "@/lib/api-error";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useToast } from "@/hooks/use-toast";
import {
  formatCreditUsd,
  formatDateTime,
  formatDayMonth,
  formatIdr,
  formatRateDate,
  formatRateE4,
  formatSpreadPct,
} from "./format";
import { WARNING_TONE } from "./labels";
import { OperatorTabs } from "./operator-tabs";
import { Pill, TopupStatusBadge } from "./status";

/**
 * Top-ups to review (RIZ-83 R21): every workspace's proofs of payment, for the
 * operator to match against the statement and approve or reject.
 *
 * **Outside the navigation on purpose.** Only allow-listed emails may use it,
 * and an entry everyone sees that answers most of them with a refusal would be
 * a menu item for nobody. The alert email links here, and the owner bookmarks
 * it. Everyone else — company admins included — gets one sentence.
 *
 * **The amount is the proof, not the image.** The page says so above the
 * table, every row's amount is bold, and a screenshot reused across top-ups is
 * flagged in the row. Approve takes two clicks for the same reason: it adds
 * money that cannot be taken back from here.
 */

const REVIEW_KEY = ["operator-topups", "review"];
const REVIEWED_KEY = ["operator-topups", "reviewed"];

async function fetchView(view: "review" | "reviewed") {
  const res = await api.get<OperatorTopupsResponse>("/operator/topups", { params: { view } });
  return (res.data.topups ?? []).filter((t): t is OperatorTopup => t != null);
}

export function OperatorTopupsPage() {
  const review = useQuery({ queryKey: REVIEW_KEY, queryFn: () => fetchView("review"), retry: false });
  const reviewed = useQuery({ queryKey: REVIEWED_KEY, queryFn: () => fetchView("reviewed"), retry: false });

  const forbidden = apiErrorStatus(review.error) === 403 || apiErrorStatus(reviewed.error) === 403;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-6 py-8">
        {/* The free-credit queue sits beside this one (RIZ-89); a
            non-operator sees neither tab. */}
        {!forbidden && <OperatorTabs active="top-ups" />}
        <h1 className="mb-1 text-2xl font-bold">Top-ups to review</h1>
        {forbidden ? (
          <Card className="mt-5 px-6 py-5">
            <p className="text-sm">This page is for Argentum operators.</p>
          </Card>
        ) : (
          <>
            <p className="mb-6 text-sm text-muted-foreground">
              Match each one against the bank or GoPay statement by its exact amount before you
              approve. Approving adds the credit at once.
            </p>
            {review.isLoading ? (
              <p className="text-sm text-muted-foreground">Loading…</p>
            ) : review.isError ? (
              <p className="text-sm text-destructive">{apiErrorMessage(review.error)}</p>
            ) : (review.data ?? []).length === 0 ? (
              <Card className="mb-4 flex flex-col items-center px-6 py-10 text-center">
                <p className="text-sm">Nothing to review.</p>
                <p className="mt-1 text-sm text-muted-foreground">
                  New top-ups show up here when a customer uploads proof of payment.
                </p>
              </Card>
            ) : (
              <ReviewTable rows={review.data ?? []} />
            )}
            <ReviewedTable rows={reviewed.data ?? []} />
          </>
        )}
      </div>
    </div>
  );
}

/** The Rate column: market, its ECB date, the spread and what it made — or the
 *  fallback, flagged, because it was priced without a market at all. */
function RateCell({ row }: { row: OperatorTopup }) {
  if (row.fx_source === "fallback" || row.fx_market_idr_per_usd_e4 == null) {
    return (
      <div className="text-xs">
        <Pill tone={WARNING_TONE}>Fallback</Pill>
        <br />
        <span className="tabular-nums">
          <b>{formatRateE4(row.fx_effective_idr_per_usd_e4)}</b>
        </span>{" "}
        <span className="text-muted-foreground">no market rate</span>
      </div>
    );
  }
  return (
    <div className="text-xs">
      <span className="tabular-nums">{formatRateE4(row.fx_market_idr_per_usd_e4)}</span>{" "}
      <span className="text-muted-foreground">market, ECB {formatRateDate(row.fx_rate_date)}</span>
      <br />
      <span className="tabular-nums">
        +{formatSpreadPct(row.fx_spread_bp ?? 0)}% → <b>{formatRateE4(row.fx_effective_idr_per_usd_e4)}</b>
      </span>
    </div>
  );
}

function ReviewTable({ rows }: { rows: OperatorTopup[] }) {
  return (
    <Card className="mb-4">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Proof uploaded</TableHead>
            <TableHead>Workspace</TableHead>
            <TableHead>Paid to</TableHead>
            <TableHead>Amount</TableHead>
            <TableHead>Credit</TableHead>
            <TableHead>Rate (IDR per $1)</TableHead>
            <TableHead>Proof</TableHead>
            <TableHead className="w-[280px]" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <ReviewRow key={row.id} row={row} />
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}

type Mode = "idle" | "confirm" | "reject";

function ReviewRow({ row }: { row: OperatorTopup }) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [mode, setMode] = useState<Mode>("idle");
  const [reason, setReason] = useState("");

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["operator-topups"] });
  };
  // A 409 is another operator — or this one's other tab — having decided it
  // first. The API's sentence says so, and the list is refreshed to show it.
  const failed = (e: unknown) => {
    toast({ title: apiErrorMessage(e), variant: "destructive" });
    setMode("idle");
    if (apiErrorStatus(e) === 409 || apiErrorStatus(e) === 404) refresh();
  };

  const approve = useMutation({
    mutationFn: async () =>
      (await api.post<TopupApproveResponse>(`/operator/topups/${row.id}/approve`)).data,
    onSuccess: () => {
      toast({ title: `Added ${formatCreditUsd(row.credit_micro_usd)} to ${row.company_name}.` });
      refresh();
    },
    onError: failed,
  });
  const reject = useMutation({
    mutationFn: async () =>
      (await api.post(`/operator/topups/${row.id}/reject`, { reason })).data,
    onSuccess: () => {
      toast({ title: `Rejected the top-up from ${row.company_name}.` });
      refresh();
    },
    onError: (e: unknown) => {
      // An empty reason is the operator's to fix in place, not a toast.
      if (apiErrorStatus(e) === 400) {
        toast({ title: apiErrorMessage(e), variant: "destructive" });
        return;
      }
      failed(e);
    },
  });

  // Opened before the request, then pointed at the link: a tab opened after
  // an await is a popup most browsers block.
  const viewProof = async () => {
    const tab = window.open("", "_blank");
    try {
      const link = (await api.get<TopupProofLinkResponse>(`/operator/topups/${row.id}/proof`)).data;
      if (tab) {
        tab.opener = null;
        tab.location.href = link.url;
      } else {
        window.open(link.url, "_blank", "noopener");
      }
    } catch (e) {
      tab?.close();
      toast({ title: apiErrorMessage(e), variant: "destructive" });
    }
  };

  return (
    <TableRow>
      <TableCell className="align-top whitespace-nowrap">{formatDateTime(row.proof_uploaded_at)}</TableCell>
      <TableCell className="align-top">
        <span className="font-medium">{row.company_name}</span>
        <p className="text-xs text-muted-foreground">{row.requested_by_email}</p>
        {row.duplicate_of && (
          <p className="mt-1 text-xs text-warning-ink">
            Same image as a {row.duplicate_of.company_name} top-up (
            {formatIdr(row.duplicate_of.expected_amount_idr)}, {formatDayMonth(row.duplicate_of.created_at)}).
          </p>
        )}
      </TableCell>
      <TableCell className="align-top">{row.payee_label || row.payee_id}</TableCell>
      <TableCell className="align-top tabular-nums whitespace-nowrap">
        <b>{formatIdr(row.expected_amount_idr)}</b>
      </TableCell>
      <TableCell className="align-top tabular-nums">{formatCreditUsd(row.credit_micro_usd)}</TableCell>
      <TableCell className="align-top">
        <RateCell row={row} />
      </TableCell>
      <TableCell className="align-top">
        <button
          type="button"
          onClick={() => void viewProof()}
          className="whitespace-nowrap font-medium text-primary underline underline-offset-2 hover:text-primary/90"
        >
          View proof
        </button>
      </TableCell>
      <TableCell className="align-top">
        {mode === "reject" ? (
          <div>
            <label htmlFor={`reason-${row.id}`} className="mb-1.5 block text-sm font-medium">
              Reason the customer will see
            </label>
            <Textarea
              id={`reason-${row.id}`}
              value={reason}
              maxLength={300}
              onChange={(e) => setReason(e.target.value)}
              className="min-h-[58px] text-[13px]"
            />
            <div className="mt-2 flex justify-end gap-2">
              <Button variant="ghost" size="sm" onClick={() => setMode("idle")}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                size="sm"
                disabled={reject.isPending || reason.trim() === ""}
                onClick={() => reject.mutate()}
              >
                Reject top-up
              </Button>
            </div>
          </div>
        ) : (
          <div className="flex justify-end gap-2">
            {mode === "confirm" ? (
              <>
                <Button size="sm" disabled={approve.isPending} onClick={() => approve.mutate()}>
                  Confirm: add {formatCreditUsd(row.credit_micro_usd)}
                </Button>
                <Button variant="ghost" size="sm" onClick={() => setMode("idle")}>
                  Cancel
                </Button>
              </>
            ) : (
              <>
                <Button size="sm" onClick={() => setMode("confirm")}>
                  Approve
                </Button>
                <Button variant="outline" size="sm" onClick={() => setMode("reject")}>
                  Reject
                </Button>
              </>
            )}
          </div>
        )}
      </TableCell>
    </TableRow>
  );
}

function ReviewedTable({ rows }: { rows: OperatorTopup[] }) {
  return (
    <Card>
      <div className="px-6 pt-5 pb-2">
        <p className="text-base font-semibold">Reviewed</p>
        <p className="mt-1 text-sm text-muted-foreground">The last 50 decisions.</p>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Reviewed</TableHead>
            <TableHead>Workspace</TableHead>
            <TableHead>Amount</TableHead>
            <TableHead>Credit</TableHead>
            <TableHead>Decision</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.id}>
              <TableCell className="whitespace-nowrap">{formatDateTime(row.reviewed_at)}</TableCell>
              <TableCell>{row.company_name}</TableCell>
              <TableCell className="tabular-nums whitespace-nowrap">{formatIdr(row.expected_amount_idr)}</TableCell>
              <TableCell className="tabular-nums">{formatCreditUsd(row.credit_micro_usd)}</TableCell>
              <TableCell>
                <TopupStatusBadge status={row.status} />{" "}
                <span className="text-xs text-muted-foreground">
                  {row.status === "rejected"
                    ? row.reject_reason
                    : row.reviewed_by_email
                      ? `by ${row.reviewed_by_email}`
                      : ""}
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
