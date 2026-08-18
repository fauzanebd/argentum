"""Reading a PDF's own text layer, and knowing when there isn't one (T-P2).

**Why there is no OCR and no layout model in this file.** A born-digital PDF —
anything exported from an ERP, an accounting package or an internet-banking
portal — carries the exact characters and their coordinates. Extraction is
deterministic, free, and correct by construction; a model pass over the same
page can only introduce error. The published benchmarks agree: on the
born-digital slice the best parser and the second are under a point apart, while
on the full mixed set they spread thirty-three. The hard cases are hard and the
ordinary ones are solved, so this file solves the ordinary ones and *classifies*
the hard ones for `T-P3` rather than guessing at them.

What it produces per page is text, word boxes and table candidates. It does not
decide types, units or headers — that is `T-P4`, in Go, beside the two locale
number parsers this product already has. A second typing implementation in a
second language is the drift the repo's own `sqlguard` promotion note warns
about.
"""

from __future__ import annotations

import io
from dataclasses import dataclass, field
from typing import Any

import pdfplumber

# A page is `needs_ocr` when its own text layer cannot be trusted. The two
# thresholds below are the whole of that judgement, and both are deliberately
# generous: classifying a readable page as needing OCR costs money in T-P3,
# while classifying a garbled page as readable puts nonsense into a tenant's
# data with nothing downstream able to tell.
#
# ALNUM_RATIO_FLOOR catches the failure that looks like success — a subsetted
# font with no ToUnicode map decodes to control characters and private-use
# glyphs, so the page "has text" and the text is garbage.
ALNUM_RATIO_FLOOR = 0.6
# MIN_CHARS_PER_PAGE is the other half: a scanned page often carries a handful
# of characters from a stamp or a header, which is not a text layer.
MIN_CHARS_PER_PAGE = 40
# Words are capped per page so one pathological page cannot produce a
# multi-megabyte artifact. 5000 is far above a dense A4 page of prose (~700) and
# far below anything that costs real storage.
MAX_WORDS_PER_PAGE = 5000

PARSER_NAME = "pdfplumber"


class PageLimitExceeded(Exception):
    """The document has more pages than the caller allows.

    Raised before any page is read: the page count is known as soon as the file
    is opened, and reading two hundred pages to then refuse them is the cost the
    limit exists to avoid.
    """

    def __init__(self, page_count: int, max_pages: int) -> None:
        super().__init__(f"document has {page_count} pages, limit is {max_pages}")
        self.page_count = page_count
        self.max_pages = max_pages


class UnreadablePDF(Exception):
    """The file could not be opened as a PDF at all."""


@dataclass
class ParsedPage:
    number: int
    kind: str
    width: float
    height: float
    char_count: int
    alnum_ratio: float
    image_area_ratio: float
    markdown: str = ""
    words: list[dict[str, Any]] = field(default_factory=list)
    tables: list[dict[str, Any]] = field(default_factory=list)
    error: str = ""


def parse_pdf(data: bytes, max_pages: int) -> dict[str, Any]:
    """Read every page and report what was found.

    A page that raises is recorded with its error and the rest of the document
    is still returned. One bad page must not cost the other forty their
    extraction — the same rule the cookbook harvester follows for one
    candidate's failed embedding.
    """
    try:
        pdf = pdfplumber.open(io.BytesIO(data))
    except Exception as exc:  # noqa: BLE001 - every failure here means the same thing
        raise UnreadablePDF(str(exc)) from exc

    with pdf:
        page_count = len(pdf.pages)
        if max_pages > 0 and page_count > max_pages:
            raise PageLimitExceeded(page_count, max_pages)

        pages: list[ParsedPage] = []
        for index, page in enumerate(pdf.pages, start=1):
            try:
                pages.append(_read_page(page, index))
            except Exception as exc:  # noqa: BLE001 - reported, not raised
                pages.append(
                    ParsedPage(
                        number=index,
                        kind="failed",
                        width=float(page.width or 0),
                        height=float(page.height or 0),
                        char_count=0,
                        alnum_ratio=0.0,
                        image_area_ratio=0.0,
                        error=str(exc)[:500],
                    )
                )

    return {
        "page_count": page_count,
        "parser": {"name": PARSER_NAME, "version": pdfplumber.__version__},
        "pages": [_page_json(p) for p in pages],
    }


def _read_page(page: Any, number: int) -> ParsedPage:
    text = page.extract_text() or ""
    char_count = len(text.strip())
    ratio = alnum_ratio(text)
    images = image_area_ratio(page)

    parsed = ParsedPage(
        number=number,
        kind=classify(char_count, ratio, images),
        width=float(page.width or 0),
        height=float(page.height or 0),
        char_count=char_count,
        alnum_ratio=round(ratio, 4),
        image_area_ratio=round(images, 4),
    )
    if parsed.kind != "text":
        # Nothing is returned for a page whose text layer failed the test. The
        # half-decoded string is worse than an empty one: it looks like content,
        # and T-P3 is what turns it into content.
        return parsed

    parsed.words = [
        {
            "text": w.get("text", ""),
            "x0": round(float(w.get("x0", 0)), 2),
            "top": round(float(w.get("top", 0)), 2),
            "x1": round(float(w.get("x1", 0)), 2),
            "bottom": round(float(w.get("bottom", 0)), 2),
        }
        for w in page.extract_words()[:MAX_WORDS_PER_PAGE]
    ]
    parsed.tables = read_tables(page)
    parsed.markdown = to_markdown(text, parsed.tables)
    return parsed


def read_tables(page: Any) -> list[dict[str, Any]]:
    """Table candidates, as a grid of strings plus the rectangle they came from.

    Candidates, not tables: whether these rows mean anything is decided in
    `T-P4` (which column is a header, what a `TOTAL` row is, what the numbers
    are) and confirmed by a person in `T-P7`. What this function owes the rest
    of the pipeline is the cells and where on the page they were, because a
    figure that cannot name its page is a figure nobody can check.
    """
    out = _candidates(page, page.find_tables(), "lines")
    if out:
        return out
    # **The fallback is not a nicety.** A ruled table is the easy case; a large
    # share of the reports this product will meet — ERP exports, statements,
    # anything laid out with tabs — draw no lines at all, and a parser that only
    # finds ruled tables reports "no tables" on a page that is nothing but a
    # table. pdfplumber's text strategy infers the grid from word alignment
    # instead, which is looser and needs the shape check in _candidates.
    settings = {"vertical_strategy": "text", "horizontal_strategy": "text"}
    return _candidates(page, page.find_tables(table_settings=settings), "text")


def _candidates(page: Any, found_tables: list[Any], strategy: str) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for index, found in enumerate(found_tables):
        rows = found.extract()
        if not rows:
            continue
        cleaned = [[(cell or "").strip() for cell in row] for row in rows]
        width = max(len(r) for r in cleaned)
        # A single-column "table" is a paragraph the detector found an edge in.
        if width < 2 or len(cleaned) < 2:
            continue
        # The text strategy will happily call two consecutive prose lines a
        # two-by-two grid. A real table has most of its rows filled to the same
        # width; prose does not, so requiring it is what separates the two
        # without a model. The ruled strategy has already been told where the
        # cells are, so it is trusted as it stands.
        if strategy == "text":
            full = sum(1 for r in cleaned if len([c for c in r if c]) >= width)
            if full / len(cleaned) < 0.6:
                continue
        out.append(
            {
                "index": index,
                # How it was found, kept because it is what a reviewer in T-P7
                # needs to know: a `text` candidate is an inference about
                # alignment, and a wrong column boundary looks exactly like a
                # right one until somebody reads the page beside it.
                "strategy": strategy,
                "bbox": [round(float(v), 2) for v in found.bbox],
                "rows": cleaned,
                "row_count": len(cleaned),
                "col_count": width,
            }
        )
    return out


def classify(char_count: int, ratio: float, images: float) -> str:
    """Decide whether this page's own text can be believed.

    Order matters: the character count is checked first because a page with no
    text has an undefined ratio, and an undefined ratio that defaults to 1.0
    would classify every scan as readable.
    """
    if char_count < MIN_CHARS_PER_PAGE:
        return "needs_ocr"
    if ratio < ALNUM_RATIO_FLOOR:
        return "needs_ocr"
    # A page that is mostly one big image *and* carries little text is a scan
    # with a caption. A page that is mostly image and carries plenty of text is
    # a report with a chart in it, and its text is fine.
    if images > 0.8 and char_count < 200:
        return "needs_ocr"
    return "text"


def alnum_ratio(text: str) -> float:
    """Share of characters that are alphanumeric, whitespace or ordinary
    punctuation.

    This is the cheap test for a broken font map. Real prose in any language
    this product serves sits well above 0.9; a page whose glyphs decode through
    a missing ToUnicode table collapses toward zero, because what comes back is
    private-use code points rather than letters.
    """
    if not text:
        return 0.0
    good = sum(1 for ch in text if ch.isalnum() or ch.isspace() or ch in ".,;:!?()[]{}%-+/*'\"&#@_=<>|\\$€£¥₹Rp")
    return good / len(text)


def image_area_ratio(page: Any) -> float:
    """How much of the page is covered by images, clamped to 1.0.

    Overlapping images would otherwise sum past 1 and read as "more than the
    whole page", and the clamp is honest because the number is only ever
    compared against a threshold.
    """
    page_area = float(page.width or 0) * float(page.height or 0)
    if page_area <= 0:
        return 0.0
    covered = 0.0
    for img in page.images:
        w = float(img.get("x1", 0)) - float(img.get("x0", 0))
        h = float(img.get("bottom", 0)) - float(img.get("top", 0))
        if w > 0 and h > 0:
            covered += w * h
    return min(covered / page_area, 1.0)


def to_markdown(text: str, tables: list[dict[str, Any]]) -> str:
    """The page as markdown: its text, then its tables as GFM pipe tables.

    The tables are appended rather than spliced into position. Splicing needs
    the text's own coordinates to know where a table interrupted it, and a
    guess at that reorders a page — which is the failure mode that makes a
    parsed document unreadable in exactly the way a reader cannot spot. `T-P8`
    chunks on this text, and a chunk in the wrong order is a citation to the
    wrong paragraph.
    """
    parts = [text.strip()] if text.strip() else []
    for table in tables:
        rows = table["rows"]
        if not rows:
            continue
        width = table["col_count"]
        header = _pad(rows[0], width)
        lines = [
            "| " + " | ".join(_escape(c) for c in header) + " |",
            "| " + " | ".join("---" for _ in range(width)) + " |",
        ]
        for row in rows[1:]:
            lines.append("| " + " | ".join(_escape(c) for c in _pad(row, width)) + " |")
        parts.append("\n".join(lines))
    return "\n\n".join(parts)


def _pad(row: list[str], width: int) -> list[str]:
    return list(row) + [""] * (width - len(row))


def _escape(cell: str) -> str:
    # A pipe inside a cell would split it into two columns. Newlines inside a
    # cell — common in wrapped headers — would end the row entirely.
    return cell.replace("|", "\\|").replace("\n", " ").strip()


def _page_json(p: ParsedPage) -> dict[str, Any]:
    return {
        "page_no": p.number,
        "kind": p.kind,
        "width": p.width,
        "height": p.height,
        "char_count": p.char_count,
        "alnum_ratio": p.alnum_ratio,
        "image_area_ratio": p.image_area_ratio,
        "markdown": p.markdown,
        "words": p.words,
        "tables": p.tables,
        "error": p.error,
    }
