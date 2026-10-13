import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { FreeCreditApproveResponse, OperatorFreeCredit } from "@argentum/api-types";

import { api } from "@/lib/api";
import { apiErrorMessage, apiErrorStatus } from "@/lib/api-error";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useToast } from "@/hooks/use-toast";
import { formatCreditUsd, formatDateTime } from "./format";
import { TOPUP_STATUS_TONES } from "./labels";
import {
  FREE_CREDIT_KEY,
  FREE_CREDIT_REVIEW_KEY,
  FREE_CREDIT_REVIEWED_KEY,
  fetchFreeCredit,
  sameIpSignal,
} from "./free-credit";
import { OperatorTabs } from "./operator-tabs";
import { Pill } from "./status";

/**
 * Free credit to approve (RIZ-89 R10): every new workspace waiting for its free
 * credit, for the operator to approve or reject.
 *
 * Outside the navigation for the top-up queue's reason — only allow-listed
 * emails may use it, and the alert email links here. Everyone else, company
 * admins included, gets one sentence and no tabs.
 *
 * **The signals are the decision.** A connected data source is the clearest
 * sign of a real business, and other sign-ups from the same address in a week
 * the clearest sign of a script; both sit in their own column so the queue can
 * be read top to bottom. Approve and Reject both take two clicks: approving
 * adds money, and a rejection is final.
 */

export function OperatorFreeCreditPage() {
  const review = useQuery({ queryKey: FREE_CREDIT_REVIEW_KEY, queryFn: () => fetchFreeCredit("review"), retry: false });
  const reviewed = useQuery({
    queryKey: FREE_CREDIT_REVIEWED_KEY,
    queryFn: () => fetchFreeCredit("reviewed"),
    retry: false,
  });

  const forbidden = apiErrorStatus(review.error) === 403 || apiErrorStatus(reviewed.error) === 403;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-6 py-8">
        {!forbidden && <OperatorTabs active="free-credit" />}
        <h1 className="mb-1 text-2xl font-bold">Free credit to approve</h1>
        {forbidden ? (
          <Card className="mt-5 px-6 py-5">
            <p className="text-sm">This page is for Argentum operators.</p>
          </Card>
        ) : (
          <>
            <p className="mb-6 text-sm text-muted-foreground">
              Every new workspace waits here for its free credit. Approve the ones that look like a real
              business. Approving adds the credit at once.
            </p>
            {review.isLoading ? (
              <p className="text-sm text-muted-foreground">Loading…</p>
            ) : review.isError ? (
              <p className="text-sm text-destructive">{apiErrorMessage(review.error)}</p>
            ) : (review.data ?? []).length === 0 ? (
              <Card className="mb-4 flex flex-col items-center px-6 py-10 text-center">
                <p className="text-sm">Nothing to approve.</p>
                <p className="mt-1 text-sm text-muted-foreground">
                  New workspaces show up here as soon as they sign up.
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

function ReviewTable({ rows }: { rows: OperatorFreeCredit[] }) {
  return (
    <Card className="mb-4">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Signed up</TableHead>
            <TableHead>Workspace</TableHead>
            <TableHead>Signals</TableHead>
            <TableHead>Free credit</TableHead>
            <TableHead className="w-[260px]" />
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

function Signals({ row }: { row: OperatorFreeCredit }) {
  const sameIp = sameIpSignal(row.same_ip_signups_7d);
  return (
    <div className="text-xs leading-normal">
      {row.has_data_source ? (
        <span className="text-positive-ink">Data source connected</span>
      ) : (
        <span className="text-muted-foreground">No data source yet</span>
      )}
      {sameIp && (
        <>
          <br />
          <span className="text-warning-ink">{sameIp}</span>
        </>
      )}
    </div>
  );
}

type Mode = "idle" | "approve" | "reject";

function ReviewRow({ row }: { row: OperatorFreeCredit }) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [mode, setMode] = useState<Mode>("idle");
  const amount = formatCreditUsd(row.amount_micro_usd);

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: FREE_CREDIT_KEY });
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
      (await api.post<FreeCreditApproveResponse>(`/operator/free-credit/${row.id}/approve`)).data,
    onSuccess: (res) => {
      // Zero added is a workspace that already held its free credit — granted
      // while approval was off, or by an older server mid-deploy. The request
      // is closed either way; the toast says which happened.
      toast({
        title:
          (res?.added_micro_usd ?? 0) > 0
            ? `Added ${formatCreditUsd(res.added_micro_usd)} free credit to ${row.company_name}.`
            : `${row.company_name} already had free credit; nothing was added.`,
      });
      refresh();
    },
    onError: failed,
  });
  const reject = useMutation({
    mutationFn: async () => (await api.post(`/operator/free-credit/${row.id}/reject`)).data,
    onSuccess: () => {
      toast({ title: `Rejected free credit for ${row.company_name}.` });
      refresh();
    },
    onError: failed,
  });

  return (
    <TableRow>
      <TableCell className="align-top whitespace-nowrap tabular-nums">{formatDateTime(row.created_at)}</TableCell>
      <TableCell className="align-top">
        <span className="font-medium">{row.company_name}</span>
        {row.requested_by_email && <p className="text-xs text-muted-foreground">{row.requested_by_email}</p>}
      </TableCell>
      <TableCell className="align-top">
        <Signals row={row} />
      </TableCell>
      <TableCell className="align-top tabular-nums">{amount}</TableCell>
      <TableCell className="align-top">
        <div className="flex justify-end gap-2">
          {mode === "approve" ? (
            <>
              <Button size="sm" disabled={approve.isPending} onClick={() => approve.mutate()}>
                Confirm: add {amount}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setMode("idle")}>
                Cancel
              </Button>
            </>
          ) : mode === "reject" ? (
            <>
              <Button variant="destructive" size="sm" disabled={reject.isPending} onClick={() => reject.mutate()}>
                Confirm: reject
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setMode("idle")}>
                Cancel
              </Button>
            </>
          ) : (
            <>
              <Button size="sm" onClick={() => setMode("approve")}>
                Approve
              </Button>
              <Button variant="outline" size="sm" onClick={() => setMode("reject")}>
                Reject
              </Button>
            </>
          )}
        </div>
      </TableCell>
    </TableRow>
  );
}

/** "Added", not "Approved", for the top-up labels' reason: what matters is that
 *  the credit is in the balance. "Not approved" is what the user is told. */
function DecisionBadge({ status }: { status: OperatorFreeCredit["status"] }) {
  return status === "approved" ? (
    <Pill tone={TOPUP_STATUS_TONES.approved}>Added</Pill>
  ) : (
    <Pill tone={TOPUP_STATUS_TONES.rejected}>Not approved</Pill>
  );
}

function ReviewedTable({ rows }: { rows: OperatorFreeCredit[] }) {
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
            <TableHead>Free credit</TableHead>
            <TableHead>Decision</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.id}>
              <TableCell className="whitespace-nowrap">{formatDateTime(row.reviewed_at)}</TableCell>
              <TableCell>{row.company_name}</TableCell>
              <TableCell className="tabular-nums">{formatCreditUsd(row.amount_micro_usd)}</TableCell>
              <TableCell>
                <DecisionBadge status={row.status} />{" "}
                {row.reviewed_by_email && (
                  <span className="text-xs text-muted-foreground">by {row.reviewed_by_email}</span>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
