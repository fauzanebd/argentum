"""The parser sidecar: bytes in, per-page JSON out (T-P2).

**It holds no credentials and no database handle**, which is the whole reason it
is a separate service rather than a library the worker calls. Every tenancy
decision stays in Go, where the tenancy tests are; this process never learns
which company a document belongs to and cannot ask.

Deployed the way `apps/render` is — its own image, a shared secret, a health
check — and with the same network posture: it needs no egress at all, because
everything it reads arrives in the request body. `T-P3` is the ticket that
changes that, and it changes it behind a flag that is off by default.
"""

from __future__ import annotations

import logging
import os

from fastapi import FastAPI, Header, HTTPException, Query, Request
from fastapi.responses import JSONResponse

from parse import PageLimitExceeded, UnreadablePDF, parse_pdf, render_pages

# Sent as `x-docparse-secret`, matching apps/render's `x-render-secret`. It is
# not the security boundary — the service is not meant to be reachable from
# outside the cluster — it is what stops something else on the network calling
# it by accident.
SECRET = os.environ.get("DOCPARSE_SHARED_SECRET", "")
# Bounds one request body. Above the API's own DOC_MAX_UPLOAD_MB default, so the
# limit a tenant meets is the one with a sentence attached rather than this one.
MAX_BODY_BYTES = int(os.environ.get("DOCPARSE_MAX_BODY_MB", "50")) * 1024 * 1024

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger("docparse")

app = FastAPI(title="argentum docparse", docs_url=None, redoc_url=None)


@app.get("/healthz")
def healthz() -> dict[str, str]:
    """Liveness, and the parser build.

    The build string is here rather than only in `/parse` because of the stale
    process this repo's gate record already caught once: a sidecar answering
    from a previous image looks exactly like a passing run, and `T-P13` records
    this value beside every score so the difference is visible as a number.
    """
    import pdfplumber

    return {"status": "ok", "parser": "pdfplumber", "version": pdfplumber.__version__}


@app.post("/parse")
async def parse(
    request: Request,
    max_pages: int = Query(default=0, ge=0),
    x_docparse_secret: str = Header(default=""),
) -> JSONResponse:
    """Parse one PDF. The body is the file itself, not a multipart form.

    Raw bytes because there is exactly one part and the caller is a Go service
    that already holds them: a multipart envelope would buy a filename this
    process has no use for.
    """
    if SECRET and x_docparse_secret != SECRET:
        raise HTTPException(status_code=401, detail="bad or missing x-docparse-secret")

    data = await request.body()
    if not data:
        raise HTTPException(status_code=400, detail="empty body")
    if len(data) > MAX_BODY_BYTES:
        raise HTTPException(status_code=413, detail="body exceeds DOCPARSE_MAX_BODY_MB")

    try:
        result = parse_pdf(data, max_pages)
    except PageLimitExceeded as exc:
        # 422 rather than 400: the request is well formed and the document is
        # refused, which is a different thing to the caller — the Go client maps
        # this onto a terminal document status with a readable sentence, and
        # nothing retries it.
        return JSONResponse(
            status_code=422,
            content={
                "error": "page_limit",
                "page_count": exc.page_count,
                "max_pages": exc.max_pages,
            },
        )
    except UnreadablePDF as exc:
        return JSONResponse(status_code=422, content={"error": "unreadable", "detail": str(exc)[:500]})

    # No filename, no tenant, no cell values in the log line. What is safe to
    # record is shape: how many pages, and how many of them this service could
    # not read.
    needs_ocr = sum(1 for p in result["pages"] if p["kind"] == "needs_ocr")
    failed = sum(1 for p in result["pages"] if p["kind"] == "failed")
    log.info(
        "parsed document pages=%d text=%d needs_ocr=%d failed=%d",
        result["page_count"],
        result["page_count"] - needs_ocr - failed,
        needs_ocr,
        failed,
    )
    return JSONResponse(status_code=200, content=result)


@app.post("/render")
async def render(
    request: Request,
    pages: str = Query(default=""),
    x_docparse_secret: str = Header(default=""),
) -> JSONResponse:
    """Render the named pages to PNG for the OCR path (T-P3).

    Separate from /parse rather than a flag on it, because the two have
    different costs and different consequences: parsing is free and stays
    inside the deployment, while a rendered page exists to be sent to a model.
    A caller that wants images has to ask for them by name, on its own request,
    with the page numbers it decided to spend money on.
    """
    if SECRET and x_docparse_secret != SECRET:
        raise HTTPException(status_code=401, detail="bad or missing x-docparse-secret")

    data = await request.body()
    if not data:
        raise HTTPException(status_code=400, detail="empty body")
    if len(data) > MAX_BODY_BYTES:
        raise HTTPException(status_code=413, detail="body exceeds DOCPARSE_MAX_BODY_MB")

    wanted: list[int] = []
    for part in pages.split(","):
        part = part.strip()
        if not part:
            continue
        try:
            wanted.append(int(part))
        except ValueError:
            raise HTTPException(status_code=400, detail="pages must be a comma-separated list of page numbers")
    if not wanted:
        raise HTTPException(status_code=400, detail="no pages requested")

    try:
        result = render_pages(data, wanted)
    except UnreadablePDF as exc:
        return JSONResponse(status_code=422, content={"error": "unreadable", "detail": str(exc)[:500]})

    # Shape only, as everywhere else in this service: how many pages were
    # rendered, never which document or what was on them.
    log.info("rendered pages requested=%d produced=%d", len(wanted), len(result["pages"]))
    return JSONResponse(status_code=200, content=result)
