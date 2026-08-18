# The parser sidecar (T-P2). Python because the mature PDF layout tools are
# Python; a separate image because apps/backend's four are static binaries on
# alpine and this one is not.
#
# It is the second non-Go image in this repository and it is far smaller than
# the first: apps/render carries a browser, this carries pdfminer and Pillow.
# No model weights, no CUDA, no GPU — the whole of T-P2 is the text layer and
# the ruling lines, and the rung above it is a decision T-P4's measurements get
# to make rather than one this Dockerfile makes in advance.
FROM python:3.12-slim-bookworm

# Pillow needs libjpeg and zlib at runtime; pdfminer.six needs neither. Nothing
# else is installed, because everything this service reads arrives in the
# request body.
RUN apt-get update && apt-get install -y --no-install-recommends \
      libjpeg62-turbo zlib1g \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Requirements first, so a code change does not invalidate the install layer.
COPY apps/docparse/requirements.txt ./requirements.txt
RUN pip install --no-cache-dir -r requirements.txt

COPY apps/docparse/*.py ./

# Not root. This process parses files a stranger wrote — a supplier's invoice, a
# bank's statement — through a C-backed image library, which is the one part of
# this repository where a malicious input has ever had a memory-safety surface
# to reach for.
RUN useradd --create-home --shell /bin/bash docparse \
    && chown -R docparse:docparse /app
USER docparse

ENV DOCPARSE_PORT=8091
EXPOSE 8091

# One worker per container. Parsing is CPU-bound and the scaling unit is the
# pod, not the process — two workers in one container compete for the same core
# and make a slow page look like a hung service.
CMD ["sh", "-c", "uvicorn main:app --host 0.0.0.0 --port ${DOCPARSE_PORT} --workers 1"]
