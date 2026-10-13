import { useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy } from "lucide-react";
import type {
  CompanyCredits,
  Topup,
  TopupOptionsResponse,
  TopupPayee,
  TopupsResponse,
} from "@argentum/api-types";

import { api } from "@/lib/api";
import { apiErrorMessage, apiErrorStatus } from "@/lib/api-error";
import { cn } from "@/lib/utils";
import { useIsAdmin } from "@/store/auth";
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
import { microToUsd } from "@/features/usage/labels";
import { grantCaption, usedUpLine } from "@/features/usage/credit-copy";
import {
  amountError,
  digitsOnly,
  formatCreditUsd,
  formatDateTime,
  formatFileSize,
  formatIdr,
  groupAccountNumber,
  groupDigits,
  parseAmountInput,
  previewCreditMicro,
} from "./format";
import { TopupStatusBadge } from "./status";

/**
 * Top up credit (RIZ-83 R17, R18): choose an amount, transfer exactly the
 * amount shown, upload the receipt, wait for review — with the workspace's
 * history underneath.
 *
 * **One page, four states**, chosen by the workspace's open request: none (the
 * amount chooser), waiting for payment (the transfer details and the upload),
 * in review (the status, and Replace proof). The server decides every number;
 * the only arithmetic here is the preview, done with the server's own integer
 * formula, and the waiting card shows the credit the server froze.
 *
 * **Admins only.** A member is told to ask an admin and the page calls nothing:
 * the options carry the payee accounts, and those are for whoever pays.
 */

const NOT_AVAILABLE = "Top-up is not available right now. Please try again later.";
const TOO_LARGE = "That file is larger than 5 MB. Upload a smaller screenshot.";
const CHOOSE_PAYEE = "Choose the account you paid to.";
const MAX_PROOF_BYTES = 5 * 1024 * 1024;

export function TopUpPage() {
  const isAdmin = useIsAdmin();
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-3xl px-6 py-8">
        <h1 className="mb-1 text-2xl font-bold">Top up credit</h1>
        <p className="mb-6 text-sm text-muted-foreground">
          Pay by bank transfer or GoPay. We add the credit once we've matched your payment, usually
          within one working day.
        </p>
        {isAdmin ? (
          <AdminTopUp />
        ) : (
          <NoticeCard>Only workspace admins can top up credit. Ask an admin on your team.</NoticeCard>
        )}
      </div>
    </div>
  );
}

function NoticeCard({ children }: { children: ReactNode }) {
  return (
    <Card className="px-6 py-5">
      <p className="text-sm">{children}</p>
    </Card>
  );
}

function AdminTopUp() {
  const options = useQuery({
    queryKey: ["topup-options"],
    queryFn: async () => (await api.get<TopupOptionsResponse>("/topups/options")).data,
    retry: false,
  });
  const topups = useQuery({
    queryKey: ["topups"],
    queryFn: async () => (await api.get<TopupsResponse>("/topups")).data.topups ?? [],
    retry: false,
  });
  const credits = useQuery({
    queryKey: ["usage-credits"],
    queryFn: async () => (await api.get<CompanyCredits>("/usage/credits")).data,
  });

  if (options.isLoading || topups.isLoading) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }
  // A failed options call is "not available" rather than an error: an older
  // API answers 404 here, and either way there is nothing this page can do.
  if (options.isError || !options.data?.enabled || topups.isError) {
    return <NoticeCard>{NOT_AVAILABLE}</NoticeCard>;
  }

  const list = (topups.data ?? []).filter((t): t is Topup => t != null);
  const open = list.find((t) => t.status === "awaiting_payment" || t.status === "in_review");

  return (
    <div className="space-y-4">
      <BalanceCard credits={credits.data} />
      {!open && <ChooseCard options={options.data} />}
      {open?.status === "awaiting_payment" && <WaitingCard topup={open} payees={options.data.payees} />}
      {open?.status === "in_review" && <InReviewCard topup={open} payees={options.data.payees} />}
      <HistoryCard topups={list} />
    </div>
  );
}

function BalanceCard({ credits }: { credits: CompanyCredits | undefined }) {
  const balance = credits ? Math.max(0, microToUsd(credits.balance_micro_usd)) : null;
  const grant = credits ? microToUsd(credits.monthly_grant_micro_usd) : 0;
  const pct = balance != null && grant > 0 ? Math.max(0, Math.min(100, (balance / grant) * 100)) : null;
  const usedUp = credits != null && credits.balance_micro_usd <= 0 && credits.monthly_grant_micro_usd > 0;
  return (
    <Card className="px-6 py-5">
      <p className="text-sm text-muted-foreground">Credit balance</p>
      <p className="mt-1 text-2xl font-semibold">{balance != null ? `$${balance.toFixed(2)}` : "—"}</p>
      {pct != null && credits && (
        <>
          <div className="mt-2.5 h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div className="h-full bg-primary" style={{ width: `${pct}%` }} />
          </div>
          <p className="mt-1.5 text-xs text-muted-foreground">{grantCaption(credits)}</p>
          {usedUp && <p className="mt-1.5 text-xs text-destructive">{usedUpLine(credits)}</p>}
        </>
      )}
    </Card>
  );
}

/* ── Choose an amount ──────────────────────────────────────────────── */

function ChooseCard({ options }: { options: TopupOptionsResponse }) {
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [serverError, setServerError] = useState<string | null>(null);
  const amount = parseAmountInput(text);
  const clientError = text === "" ? null : amountError(amount, options);
  const error = clientError ?? serverError;

  const create = useMutation({
    mutationFn: async (amountIdr: number) =>
      (await api.post<Topup>("/topups", { amount_idr: amountIdr })).data,
    onSuccess: () => {
      setText("");
      void qc.invalidateQueries({ queryKey: ["topups"] });
    },
    onError: (e: unknown) => {
      // Another tab opened one: show it rather than a refusal.
      if (apiErrorStatus(e) === 409) {
        void qc.invalidateQueries({ queryKey: ["topups"] });
        return;
      }
      setServerError(apiErrorMessage(e, NOT_AVAILABLE));
    },
  });

  const setAmount = (raw: string) => {
    setServerError(null);
    setText(groupDigits(raw));
  };

  const range = `Between ${formatIdr(options.min_idr)} and ${formatIdr(options.max_idr)}, in whole thousands.`;
  const canContinue = amount != null && clientError == null && !create.isPending;

  return (
    <Card className="px-6 py-5">
      <p className="text-base font-semibold">How much do you want to add?</p>
      <p className="mt-1 text-sm text-muted-foreground">
        Credit pays for chat, scheduled reports and API calls in this workspace.
      </p>
      <div className="mt-4 flex flex-wrap gap-2">
        {(options.presets_idr ?? []).map((p) => (
          <button
            key={p}
            type="button"
            onClick={() => setAmount(String(p))}
            className={cn(
              "inline-flex h-8 items-center rounded-full border border-input bg-background px-3.5 text-sm tabular-nums transition-colors hover:bg-accent/10",
              amount === p && "border-primary",
            )}
          >
            {formatIdr(p)}
          </button>
        ))}
      </div>
      <label htmlFor="topup-amount" className="mt-4 mb-1.5 block text-sm font-medium">
        Amount
      </label>
      <div
        className={cn(
          "flex h-10 w-72 max-w-full items-center gap-2.5 rounded-md border bg-background px-3 focus-within:ring-2 focus-within:ring-ring",
          error ? "border-destructive focus-within:ring-destructive" : "border-input",
        )}
      >
        <span className="text-muted-foreground">Rp</span>
        <input
          id="topup-amount"
          inputMode="numeric"
          autoComplete="off"
          value={text}
          onChange={(e) => setAmount(e.target.value)}
          aria-invalid={error != null}
          aria-describedby="topup-amount-help"
          className="min-w-0 flex-1 bg-transparent text-[15px] font-medium tabular-nums outline-none"
        />
      </div>
      {error ? (
        <p id="topup-amount-help" role="alert" className="mt-1.5 text-sm text-destructive">
          {error}
        </p>
      ) : (
        <p id="topup-amount-help" className="mt-1.5 text-xs">
          {amount != null && (
            <>
              You get{" "}
              <b>{formatCreditUsd(previewCreditMicro(amount, options.preview_idr_per_usd_e4))}</b>{" "}
              credit at today's rate.{" "}
            </>
          )}
          {range}
        </p>
      )}
      <div className="mt-5 flex items-center gap-3">
        <span className="text-xs text-muted-foreground">Top-ups are not refundable.</span>
        <span className="flex-1" />
        <Button disabled={!canContinue} onClick={() => amount != null && create.mutate(amount)}>
          Continue
        </Button>
      </div>
    </Card>
  );
}

/* ── Waiting for payment ───────────────────────────────────────────── */

function CopyButton({ value, label, small }: { value: string; label: string; small?: boolean }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className={cn(small ? "h-7 px-2.5 text-xs" : "h-8 px-3 text-[13px]")}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        } catch {
          // A browser that refuses the clipboard still shows the value to
          // copy by hand; there is nothing useful to say about it.
        }
      }}
    >
      {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
      {label}
    </Button>
  );
}

/** "Rp 100.347" with the 3-digit code marked: the part people forget. */
function ExpectedAmount({ topup }: { topup: Topup }) {
  const thousands = groupDigits(Math.floor(topup.amount_idr / 1000));
  const code = String(topup.unique_code).padStart(3, "0");
  return (
    <span className="text-[28px] font-bold tracking-tight tabular-nums">
      Rp {thousands}.<mark className="rounded bg-warning-tint px-0.5 text-inherit">{code}</mark>
    </span>
  );
}

function WaitingCard({ topup, payees }: { topup: Topup; payees: TopupPayee[] }) {
  const qc = useQueryClient();
  const cancel = useMutation({
    mutationFn: async () => (await api.post<Topup>(`/topups/${topup.id}/cancel`)).data,
    onSettled: () => void qc.invalidateQueries({ queryKey: ["topups"] }),
  });
  return (
    <Card className="px-6 py-5">
      <div className="flex items-center gap-3">
        <TopupStatusBadge status={topup.status} />
        <span className="flex-1" />
        <span className="text-xs text-muted-foreground">
          {formatCreditUsd(topup.credit_micro_usd)} credit
        </span>
      </div>
      <p className="mt-3.5 mb-1.5 text-sm font-medium">Transfer exactly</p>
      <div className="flex flex-wrap items-center gap-3">
        <ExpectedAmount topup={topup} />
        <CopyButton value={String(topup.expected_amount_idr)} label="Copy amount" />
      </div>
      <p className="mt-1 text-sm text-muted-foreground">
        Include the last 3 digits: they are how we match your transfer to this request. Pay by{" "}
        {formatDateTime(topup.expires_at)} or this request expires.
      </p>
      <p className="mt-4 mb-1.5 text-sm font-medium">Pay to any of these accounts</p>
      <ul className="mt-3 divide-y divide-border rounded-lg border border-border">
        {payees.map((p) => (
          <li key={p.id} className="flex flex-wrap items-center gap-3 px-3.5 py-2.5">
            <span className="w-24 text-[13px] font-semibold">{p.label}</span>
            <span className="font-mono text-sm tracking-wide">{groupAccountNumber(p.account_number)}</span>
            <span className="text-xs text-muted-foreground">{p.account_name}</span>
            <span className="flex-1" />
            <CopyButton value={digitsOnly(p.account_number)} label="Copy" small />
          </li>
        ))}
      </ul>
      <div className="my-5 border-t border-border" />
      <p className="text-base font-semibold">Upload proof of payment</p>
      <ProofForm
        topup={topup}
        payees={payees}
        secondary={
          <Button variant="ghost" disabled={cancel.isPending} onClick={() => cancel.mutate()}>
            Cancel request
          </Button>
        }
      />
      <p className="mt-3 text-xs leading-relaxed text-muted-foreground">
        We check every top-up against our bank and GoPay statements. A screenshot on its own does
        not add credit.
      </p>
    </Card>
  );
}

/**
 * Paid to, the drop zone and Submit for review — the waiting card's upload,
 * and the in-review card's Replace proof.
 *
 * A refused upload keeps the chosen file on screen with the API's sentence
 * under it, and Submit stays disabled until the file changes: sending the same
 * bytes again would be refused the same way.
 */
function ProofForm({
  topup,
  payees,
  secondary,
  onDone,
}: {
  topup: Topup;
  payees: TopupPayee[];
  secondary: ReactNode;
  onDone?: () => void;
}) {
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [payeeId, setPayeeId] = useState(topup.payee_id ?? "");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [blocked, setBlocked] = useState(false);
  const [dragging, setDragging] = useState(false);

  const upload = useMutation({
    mutationFn: async (f: File) => {
      const form = new FormData();
      form.append("payee_id", payeeId);
      form.append("file", f);
      return (await api.post<Topup>(`/topups/${topup.id}/proof`, form)).data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["topups"] });
      onDone?.();
    },
    onError: (e: unknown) => {
      setError(apiErrorMessage(e));
      setBlocked(true);
      // A closed request has moved on: show the page as it now is.
      if (apiErrorStatus(e) === 409) void qc.invalidateQueries({ queryKey: ["topups"] });
    },
  });

  const choose = (f: File | null | undefined) => {
    if (!f) return;
    setFile(f);
    setError(null);
    setBlocked(false);
  };

  const submit = () => {
    if (!file) return;
    if (!payeeId) {
      setError(CHOOSE_PAYEE);
      return;
    }
    // Refused here rather than sent: the server refuses it too, but a 7 MB
    // body cut off mid-upload can reach the browser as a dropped connection
    // instead of the 413 that explains it.
    if (file.size > MAX_PROOF_BYTES) {
      setError(TOO_LARGE);
      setBlocked(true);
      return;
    }
    setError(null);
    upload.mutate(file);
  };

  return (
    <div>
      <label htmlFor={`paid-to-${topup.id}`} className="mt-3.5 mb-1.5 block text-sm font-medium">
        Paid to
      </label>
      <select
        id={`paid-to-${topup.id}`}
        value={payeeId}
        onChange={(e) => {
          setPayeeId(e.target.value);
          if (error === CHOOSE_PAYEE) setError(null);
        }}
        className="h-9 w-60 max-w-full rounded-md border border-input bg-background px-3 text-sm"
      >
        <option value="" disabled>
          {CHOOSE_PAYEE}
        </option>
        {payees.map((p) => (
          <option key={p.id} value={p.id}>
            {p.label}
          </option>
        ))}
      </select>
      <input
        ref={input}
        type="file"
        accept="image/png,image/jpeg,image/webp,application/pdf"
        className="hidden"
        data-testid="proof-file"
        onChange={(e) => {
          choose(e.target.files?.[0]);
          e.target.value = "";
        }}
      />
      <div
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragging(false);
          choose(e.dataTransfer.files?.[0]);
        }}
        className={cn(
          "mt-3.5 rounded-lg border-[1.5px] border-dashed px-5 py-5 text-center",
          error && file ? "border-destructive bg-destructive/5" : "border-border bg-inset",
          dragging && "border-primary",
        )}
      >
        {file ? (
          <p className="text-sm">
            <b>{file.name}</b> <span className="text-muted-foreground">{formatFileSize(file.size)}</span>{" "}
            <LinkButton onClick={() => input.current?.click()}>choose another file</LinkButton>
          </p>
        ) : (
          <>
            <p className="text-sm">
              Drop a screenshot or PDF of the transfer here, or{" "}
              <LinkButton onClick={() => input.current?.click()}>choose a file</LinkButton>
            </p>
            <p className="mt-1 text-xs text-muted-foreground">PNG, JPG, WebP or PDF, up to 5 MB.</p>
          </>
        )}
      </div>
      {error && (
        <p role="alert" className="mt-2 text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="mt-4 flex items-center gap-3">
        <Button disabled={!file || blocked || upload.isPending} onClick={submit}>
          {upload.isPending ? "Uploading…" : "Submit for review"}
        </Button>
        {secondary}
      </div>
    </div>
  );
}

function LinkButton({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="font-medium text-primary underline underline-offset-2 hover:text-primary/90"
    >
      {children}
    </button>
  );
}

/* ── In review ─────────────────────────────────────────────────────── */

function InReviewCard({ topup, payees }: { topup: Topup; payees: TopupPayee[] }) {
  const [replacing, setReplacing] = useState(false);
  const paidTo = payees.find((p) => p.id === topup.payee_id)?.label ?? topup.payee_id ?? "";
  return (
    <Card className="px-6 py-5">
      <TopupStatusBadge status={topup.status} />
      <p className="mt-3 text-base font-semibold tabular-nums">
        {formatIdr(topup.expected_amount_idr)} for {formatCreditUsd(topup.credit_micro_usd)} credit
      </p>
      <p className="mt-1 text-sm text-muted-foreground">
        Proof uploaded {formatDateTime(topup.proof_uploaded_at)}, paid to {paidTo}. We'll add the
        credit once we've matched your payment, usually within one working day.
      </p>
      {replacing ? (
        <ProofForm
          topup={topup}
          payees={payees}
          onDone={() => setReplacing(false)}
          secondary={
            <Button variant="ghost" onClick={() => setReplacing(false)}>
              Cancel
            </Button>
          }
        />
      ) : (
        <div className="mt-3.5">
          <Button variant="outline" size="sm" className="h-8 px-3 text-[13px]" onClick={() => setReplacing(true)}>
            Replace proof
          </Button>
        </div>
      )}
    </Card>
  );
}

/* ── History ───────────────────────────────────────────────────────── */

function HistoryCard({ topups }: { topups: Topup[] }) {
  if (topups.length === 0) {
    return (
      <Card className="px-6 py-5">
        <p className="text-base font-semibold">Your top-ups</p>
        <p className="mt-2.5 text-sm text-muted-foreground">No top-ups yet.</p>
      </Card>
    );
  }
  return (
    <Card>
      <div className="px-6 pt-5 pb-2">
        <p className="text-base font-semibold">Your top-ups</p>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Requested</TableHead>
            <TableHead>Amount</TableHead>
            <TableHead>Credit</TableHead>
            <TableHead>Status</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {topups.map((t) => (
            <TableRow key={t.id}>
              <TableCell className="align-top">{formatDateTime(t.created_at)}</TableCell>
              <TableCell className="align-top tabular-nums whitespace-nowrap">
                {formatIdr(t.expected_amount_idr)}
              </TableCell>
              <TableCell className="align-top tabular-nums">{formatCreditUsd(t.credit_micro_usd)}</TableCell>
              <TableCell className="align-top">
                <TopupStatusBadge status={t.status} />
                {t.status === "rejected" && t.reject_reason && (
                  <p className="mt-1 text-xs text-muted-foreground">{t.reject_reason}</p>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
