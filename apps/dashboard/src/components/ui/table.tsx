import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * The table primitive (T-U11).
 *
 * `components/ui/` had no table, so every data surface in the dashboard — the
 * usage tabs, documents, API keys — was a stack of `div`s with `divide-y` and
 * its own idea of column widths. None of them aligned with each other, and a
 * change to row density was a change to nine files.
 *
 * `Table` always renders inside its own horizontally-scrolling wrapper. A wide
 * table is the most common way a page acquires a horizontal scrollbar on
 * mobile, and putting the overflow container here rather than at each call site
 * is what stops that being rediscovered per screen.
 */

const Table = React.forwardRef<
  HTMLTableElement,
  React.HTMLAttributes<HTMLTableElement>
>(({ className, ...props }, ref) => (
  <div className="relative w-full overflow-x-auto">
    <table
      ref={ref}
      className={cn("w-full caption-bottom text-[13px]", className)}
      {...props}
    />
  </div>
));
Table.displayName = "Table";

const TableHeader = React.forwardRef<
  HTMLTableSectionElement,
  React.HTMLAttributes<HTMLTableSectionElement>
>(({ className, ...props }, ref) => (
  <thead ref={ref} className={cn("[&_tr]:border-b", className)} {...props} />
));
TableHeader.displayName = "TableHeader";

const TableBody = React.forwardRef<
  HTMLTableSectionElement,
  React.HTMLAttributes<HTMLTableSectionElement>
>(({ className, ...props }, ref) => (
  <tbody
    ref={ref}
    className={cn("[&_tr:last-child]:border-0", className)}
    {...props}
  />
));
TableBody.displayName = "TableBody";

const TableRow = React.forwardRef<
  HTMLTableRowElement,
  React.HTMLAttributes<HTMLTableRowElement>
>(({ className, ...props }, ref) => (
  <tr
    ref={ref}
    className={cn(
      "border-b border-border transition-colors hover:bg-secondary data-[state=selected]:bg-primary-tint",
      className,
    )}
    {...props}
  />
));
TableRow.displayName = "TableRow";

const TableHead = React.forwardRef<
  HTMLTableCellElement,
  React.ThHTMLAttributes<HTMLTableCellElement>
>(({ className, ...props }, ref) => (
  <th
    ref={ref}
    className={cn(
      // 10px/650 uppercase is the label tier from the T-U1 type scale — the one
      // weight in the system that exists to be read as a column header rather
      // than as content.
      "h-8 px-3 text-left align-middle text-[10px] font-semibold uppercase tracking-wide text-muted-subtle [&:has([role=checkbox])]:pr-0",
      className,
    )}
    {...props}
  />
));
TableHead.displayName = "TableHead";

const TableCell = React.forwardRef<
  HTMLTableCellElement,
  React.TdHTMLAttributes<HTMLTableCellElement>
>(({ className, ...props }, ref) => (
  <td
    ref={ref}
    className={cn("px-3 py-2 align-middle", className)}
    {...props}
  />
));
TableCell.displayName = "TableCell";

const TableEmpty = ({ children }: { children: React.ReactNode }) => (
  <tr>
    <td
      // Spans every column without being told how many there are. `100%` is not
      // valid for colSpan, but a number larger than any real column count is,
      // and browsers clamp it to the actual width.
      colSpan={999}
      className="px-3 py-8 text-center text-[13px] text-muted-foreground"
    >
      {children}
    </td>
  </tr>
);
TableEmpty.displayName = "TableEmpty";

export {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
  TableEmpty,
};
