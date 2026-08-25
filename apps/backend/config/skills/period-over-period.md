---
name: Period-over-period comparison
when_to_use: The user asks how a figure moved against an earlier period — "vs last month", "year on year", "is it up or down", "dibanding bulan lalu".
---

Two windows, named out loud before either is queried.

1. **Fix both windows first.** "Last month vs the month before" against a
   question asked on 3 March means February and January in full — not the three
   days of March that have happened. Write both ranges into the reply.
2. **Say whether the current window is complete.** A period still running is
   compared like for like or not at all: either compare the same elapsed slice
   of each (1–3 March against 1–3 February) and say that is what you did, or
   use the last two complete periods and say that instead. Silently comparing
   three days against twenty-eight is the failure this procedure exists for.
3. **One query per source, both windows in it.** A CASE or a FILTER over a
   single scan gives two figures that cannot drift apart; two round trips give
   two figures nobody can reconcile if the data moves between them.
4. **Report the absolute change and the percentage, in that order.** The
   percentage alone hides the size: a 300% rise on four orders is four orders.
5. **A prior window of zero is not a percentage.** If the earlier period has no
   rows, say it started from nothing — do not divide, and do not write "+100%"
   or "∞". This is the zero-row rule applied to the denominator, and it is the
   one arithmetic step in this procedure that can invent a figure.
6. **Do not chart it unless a chart was requested.** A direction is a sentence.
