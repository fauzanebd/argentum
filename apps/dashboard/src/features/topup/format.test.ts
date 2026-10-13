import { describe, expect, it } from "vitest";
import {
  amountError,
  digitsOnly,
  formatCreditUsd,
  formatDateTime,
  formatIdr,
  formatRateDate,
  formatRateE4,
  formatSpreadPct,
  groupAccountNumber,
  groupDigits,
  parseAmountInput,
  previewCreditMicro,
} from "./format";

const limits = { min_idr: 20_000, max_idr: 5_000_000, step_idr: 1000 };

describe("rupiah and dollars (R17)", () => {
  it("groups by thousands with a dot", () => {
    expect(groupDigits(150000)).toBe("150.000");
    expect(groupDigits("1500000")).toBe("1.500.000");
    expect(groupDigits("abc12x3")).toBe("123");
    expect(groupDigits("")).toBe("");
    expect(formatIdr(100347)).toBe("Rp 100.347");
    expect(formatIdr(5_000_000)).toBe("Rp 5.000.000");
  });

  it("rounds a credit down to the cent", () => {
    expect(formatCreditUsd(5_000_000)).toBe("$5.00");
    expect(formatCreditUsd(5_763_831)).toBe("$5.76");
    expect(formatCreditUsd(8_928_571)).toBe("$8.92");
    expect(formatCreditUsd(9_999)).toBe("$0.00");
  });
});

describe("the amount field (AC22)", () => {
  it("keeps digits only", () => {
    expect(parseAmountInput("150.000")).toBe(150_000);
    expect(parseAmountInput("Rp 150,000")).toBe(150_000);
    expect(parseAmountInput("")).toBeNull();
    expect(parseAmountInput("abc")).toBeNull();
  });

  it("refuses with the server's sentences, in the server's order", () => {
    expect(amountError(19_000, limits)).toBe("The minimum top-up is Rp 20.000.");
    expect(amountError(7_500_000, limits)).toBe("The maximum top-up is Rp 5.000.000.");
    expect(amountError(5_001_000, limits)).toBe("The maximum top-up is Rp 5.000.000.");
    expect(amountError(150_500, limits)).toBe("Enter an amount in whole thousands of rupiah.");
    expect(amountError(null, limits)).toBe("Enter an amount in whole thousands of rupiah.");
    expect(amountError(20_000, limits)).toBeNull();
    expect(amountError(5_000_000, limits)).toBeNull();
    expect(amountError(150_000, limits)).toBeNull();
  });
});

describe("the credit preview is the server's integer formula (Q28)", () => {
  it.each([
    [20_000, 173_495_700, 1_152_766],
    [100_000, 173_495_700, 5_763_831],
    [150_000, 173_495_700, 8_645_747],
    [5_000_000, 173_495_700, 288_191_580],
    [150_000, 168_000_000, 8_928_571],
    [100_000, 180_000_000, 5_555_555],
  ])("Rp %i at %i → %i micro-USD", (amount, rate, micro) => {
    expect(previewCreditMicro(amount, rate)).toBe(micro);
  });

  it("previews nothing without a rate", () => {
    expect(previewCreditMicro(100_000, 0)).toBe(0);
  });

  it("reads $8.92 for Rp 150.000 at 16.000 + 5% (Q17)", () => {
    expect(formatCreditUsd(previewCreditMicro(150_000, 168_000_000))).toBe("$8.92");
  });
});

describe("accounts, rates and times", () => {
  it("groups an account number for reading and copies digits only", () => {
    expect(groupAccountNumber("123456789012345")).toBe("1234 5678 9012 345");
    expect(groupAccountNumber("081200000000")).toBe("0812 0000 0000");
    expect(digitsOnly("0812 0000 0000")).toBe("081200000000");
  });

  it("formats a rate the Indonesian way", () => {
    expect(formatRateE4(165_234_000)).toBe("16.523,40");
    expect(formatRateE4(173_495_700)).toBe("17.349,57");
    expect(formatRateE4(180_000_000)).toBe("18.000,00");
    expect(formatSpreadPct(500)).toBe("5");
    expect(formatSpreadPct(250)).toBe("2,5");
    expect(formatSpreadPct(75)).toBe("0,75");
  });

  it("reads an ECB date as the day it names", () => {
    expect(formatRateDate("2026-10-12")).toBe("12 Oct");
    expect(formatRateDate(null)).toBe("");
  });

  it("formats a time in local time", () => {
    const local = new Date(2026, 9, 15, 14, 20).toISOString();
    expect(formatDateTime(local)).toBe("15 Oct 2026, 14:20");
    expect(formatDateTime(null)).toBe("");
  });
});
